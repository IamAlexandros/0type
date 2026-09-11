// Package ui implements 0type's floating overlay window: a small,
// Raycast-style panel positioned near the bottom-center of the screen,
// with no taskbar/alt-tab presence.
//
// Positioning approach: GNOME's compositor (Mutter) does not implement the
// wlr-layer-shell protocol (a wlroots-ecosystem extension; see
// docs/SETUP.md for the confirmed upstream status), so the originally
// planned gtk4-layer-shell approach doesn't work on GNOME/Wayland at all.
// Instead this runs the GTK4 window through Xwayland (forcing GDK's X11
// backend) and uses a direct, hand-written cgo binding to plain Xlib to
// mark the window override-redirect and position it in screen
// coordinates -- the same decades-old technique X11 launchers/OSDs have
// always used to bypass window-manager placement and decoration. This is
// actually more portable than layer-shell would have been: it works on
// GNOME as well as wlroots compositors and KDE, anywhere Xwayland is
// present (effectively everywhere).
package ui

/*
#cgo pkg-config: gtk4 x11
#cgo LDFLAGS: -lm
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <X11/Xlib.h>
#include <glib-unix.h>
#include <stdlib.h>
#include <stdio.h>
#include <math.h>
#include <pango/pangocairo.h>

// goSourceTrampoline is exported below; source_trampoline_c adapts it to
// GLib's GSourceFunc signature, passing the cgo.Handle value through as a
// guintptr (an integer the same size as a pointer, per Go's documented
// cgo.Handle idiom) rather than as a real pointer.
extern gboolean goSourceTrampoline(guintptr handle);

static gboolean source_trampoline_c(gpointer data) {
	return goSourceTrampoline((guintptr)data);
}

static guint schedule_idle_source(guintptr handle) {
	return g_idle_add(source_trampoline_c, (gpointer)handle);
}

static guint schedule_unix_signal(int signum, guintptr handle) {
	return g_unix_signal_add(signum, source_trampoline_c, (gpointer)handle);
}

// Everything below is typed in terms of plain GtkWidget* at the Go
// boundary; the GTK_WINDOW()/GTK_BOX()/GTK_LABEL() type-cast macros are
// applied here in C, so the Go side never has to reason about GObject's
// upcast/downcast pointer typing.

static GtkWidget *new_window(void) {
	return gtk_window_new();
}

static GtkWidget *new_box_vertical(void) {
	return gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
}

// SlideState is the whole content of the bar: a GtkDrawingArea (not a
// GtkLabel -- every attempt to get a fixed-size GTK *container* to hold a
// growing label without its natural size leaking into the panel hit the
// same "size_request is a floor, not a ceiling" trap; a drawing area has
// no children for anything to leak from) painted entirely with
// Cairo/Pango in slide_draw as three regions that stay put across every
// state, Raycast-bar style:
//
//   [ brand mark ]  [ ........ content area ........ ]  [ level bars ]
//
// The mark is the product's "0" in a small rounded tile -- an icon, not a
// centered wordmark: a lone word in an empty pill read as a splash
// screen, not a tool. The content area shows a muted placeholder while
// idle, the sliding transcript while dictating, and the "Copied" message
// at the end. The bars are a live meter of the actual mic level (fed by
// Window.SetLevel from the capture pipeline), so the bar visibly reacts
// to speech without any decorative pulsing.
typedef struct {
	GtkWidget *area;
	char *text;
	double current_x;
	double target_x;
	gint64 last_time;
	gboolean started;
	gboolean confirmation; // TRUE while showing "Copied" rather than idle/transcript text
	double level_target;   // 0..1, from the capture pipeline
	double level_current;  // eased toward level_target each frame (slide_tick)
} SlideState;

// Geometry of the three regions, in the drawing area's logical pixels.
#define MARK_SIZE   22.0
#define MARK_RADIUS  6.0
#define MARK_GAP    12.0
#define BAR_COUNT    3
#define BAR_W        3.0
#define BAR_GAP      3.0
#define BARS_GAP    12.0
#define BARS_W      (BAR_COUNT * BAR_W + (BAR_COUNT - 1) * BAR_GAP)
#define CONTENT_X   (MARK_SIZE + MARK_GAP)
#define CONTENT_W(width) ((width) - CONTENT_X - BARS_GAP - BARS_W)

// The mark must read as a *digit* -- the name is a pun on zero -- so it
// uses Adwaita Mono's dotted zero; in a proportional UI face the "0" is a
// plain oval and reads as the letter O.
#define MARK_GLYPH       "0"
#define MARK_FONT_FAMILY "Adwaita Mono"
#define MARK_FONT_PT     13
#define IDLE_TEXT        "Listening…"
#define FADE_W           40.0

static void rounded_rect(cairo_t *cr, double x, double y, double w, double h, double r) {
	cairo_new_sub_path(cr);
	cairo_arc(cr, x + w - r, y + r, r, -G_PI / 2, 0);
	cairo_arc(cr, x + w - r, y + h - r, r, 0, G_PI / 2);
	cairo_arc(cr, x + r, y + h - r, r, G_PI / 2, G_PI);
	cairo_arc(cr, x + r, y + r, r, G_PI, 3 * G_PI / 2);
	cairo_close_path(cr);
}

// draw_mark paints the brand tile at the left edge: a subtle rounded
// square holding the accent "0" -- or, during the confirmation, a green
// check, so the whole bar reads as "done" at a glance.
static void draw_mark(GtkWidget *area, cairo_t *cr, int height, gboolean confirmed) {
	double y = (height - MARK_SIZE) / 2.0;
	if (confirmed) {
		cairo_set_source_rgba(cr, 0.54, 0.86, 0.68, 0.18);
	} else {
		cairo_set_source_rgba(cr, 1, 1, 1, 0.06);
	}
	rounded_rect(cr, 0.5, y + 0.5, MARK_SIZE - 1, MARK_SIZE - 1, MARK_RADIUS);
	cairo_fill_preserve(cr);
	cairo_set_source_rgba(cr, 1, 1, 1, 0.08);
	cairo_set_line_width(cr, 1.0);
	cairo_stroke(cr);

	PangoLayout *layout = gtk_widget_create_pango_layout(area, confirmed ? "✓" : MARK_GLYPH);
	if (!confirmed) {
		PangoFontDescription *desc = pango_font_description_new();
		pango_font_description_set_family(desc, MARK_FONT_FAMILY);
		pango_font_description_set_size(desc, MARK_FONT_PT * PANGO_SCALE);
		pango_font_description_set_weight(desc, PANGO_WEIGHT_MEDIUM);
		pango_layout_set_font_description(layout, desc);
		pango_font_description_free(desc);
	}
	int tw, th;
	pango_layout_get_pixel_size(layout, &tw, &th);
	if (confirmed) {
		cairo_set_source_rgba(cr, 0.54, 0.86, 0.68, 0.95);
	} else {
		cairo_set_source_rgba(cr, 0.55, 0.62, 1.0, 0.92);
	}
	cairo_move_to(cr, (MARK_SIZE - tw) / 2.0, y + (MARK_SIZE - th) / 2.0);
	pango_cairo_show_layout(cr, layout);
	g_object_unref(layout);
}

// draw_bars paints the live level meter at the right edge: three thin
// bars whose heights follow the (eased) mic level. Muted, so it's an
// affordance that the bar is listening, not a feature.
static void draw_bars(cairo_t *cr, int width, int height, double level) {
	const double min_h = 6.0, max_h = 16.0;
	// The middle bar leads and the outer two lag slightly, so it reads as
	// a signal rather than three identical sliders.
	const double scale[BAR_COUNT] = {0.7, 1.0, 0.85};
	double x0 = width - BARS_W;
	for (int i = 0; i < BAR_COUNT; i++) {
		double h = min_h + (max_h - min_h) * level * scale[i];
		double x = x0 + i * (BAR_W + BAR_GAP);
		double y = (height - h) / 2.0;
		cairo_set_source_rgba(cr, 1, 1, 1, 0.22 + 0.35 * level);
		rounded_rect(cr, x, y, BAR_W, h, BAR_W / 2.0);
		cairo_fill(cr);
	}
}

// draw_content paints whatever belongs between the mark and the bars,
// clipped to that area so sliding text never runs under either: the
// muted idle placeholder, the transcript (with a short fade-to-gray at
// its left edge, so words sliding out read as receding rather than being
// cut off), or the confirmation message.
static void draw_content(GtkWidget *area, cairo_t *cr, int width, int height, SlideState *s) {
	double cw = CONTENT_W(width);
	cairo_save(cr);
	cairo_rectangle(cr, CONTENT_X, 0, cw, height);
	cairo_clip(cr);

	gboolean idle = (s->text == NULL || s->text[0] == '\0');
	const char *text = idle ? IDLE_TEXT : s->text;
	PangoLayout *layout = gtk_widget_create_pango_layout(area, text);
	pango_layout_set_single_paragraph_mode(layout, TRUE);
	if (idle) {
		PangoFontDescription *desc = pango_font_description_copy(pango_context_get_font_description(gtk_widget_get_pango_context(area)));
		pango_font_description_set_weight(desc, PANGO_WEIGHT_NORMAL);
		pango_layout_set_font_description(layout, desc);
		pango_font_description_free(desc);
	}
	int tw, th;
	pango_layout_get_pixel_size(layout, &tw, &th);
	double y = (height - th) / 2.0;

	if (idle) {
		cairo_set_source_rgba(cr, 0.50, 0.52, 0.60, 0.85);
		cairo_move_to(cr, CONTENT_X + (cw - tw) / 2.0, y);
	} else if (s->confirmation) {
		cairo_set_source_rgba(cr, 0.54, 0.86, 0.68, 0.95);
		cairo_move_to(cr, CONTENT_X + (cw - tw) / 2.0, y);
	} else {
		GdkRGBA color;
		gtk_widget_get_color(area, &color);
		cairo_pattern_t *g = cairo_pattern_create_linear(CONTENT_X, 0, CONTENT_X + FADE_W, 0);
		cairo_pattern_add_color_stop_rgba(g, 0.0, 0.50, 0.50, 0.54, color.alpha * 0.45);
		cairo_pattern_add_color_stop_rgba(g, 1.0, color.red, color.green, color.blue, color.alpha);
		cairo_set_source(cr, g);
		cairo_pattern_destroy(g); // cairo_set_source holds its own reference
		cairo_move_to(cr, s->current_x, y);
	}
	pango_cairo_show_layout(cr, layout);
	g_object_unref(layout);
	cairo_restore(cr);
}

static void slide_draw(GtkDrawingArea *area, cairo_t *cr, int width, int height, gpointer data) {
	SlideState *s = (SlideState *)data;
	draw_content(GTK_WIDGET(area), cr, width, height, s);
	draw_mark(GTK_WIDGET(area), cr, height, s->confirmation);
	draw_bars(cr, width, height, s->level_current);
}

// slide_tick eases current_x toward target_x (and the level meter toward
// its target) by a fraction of the remaining distance each frame, scaled
// by actual elapsed time via a simple exponential ease -- genuinely
// smooth, frame-clock synced, and automatically continuous if the target
// changes again before a previous move finishes.
static gboolean slide_tick(GtkWidget *widget, GdkFrameClock *clock, gpointer data) {
	SlideState *s = (SlideState *)data;

	gint64 now = gdk_frame_clock_get_frame_time(clock);
	double dt = 1.0 / 60.0;
	if (s->last_time != 0) {
		dt = (now - s->last_time) / 1000000.0;
		if (dt <= 0) {
			dt = 1.0 / 60.0;
		} else if (dt > 0.1) {
			dt = 0.1; // clamp a long gap (e.g. window was hidden) to avoid a visible jump
		}
	}
	s->last_time = now;

	double diff = s->target_x - s->current_x;
	if (fabs(diff) < 0.25) {
		s->current_x = s->target_x;
	} else {
		s->current_x += diff * (1.0 - exp(-dt / 0.11)); // tau 110ms: snappy but not abrupt
	}
	s->level_current += (s->level_target - s->level_current) * (1.0 - exp(-dt / 0.06));

	gtk_widget_queue_draw(s->area);
	return G_SOURCE_CONTINUE;
}

// new_slide_area creates the fixed-size bar content and its animation
// state together, and starts the per-frame tick callback that drives it
// for the window's lifetime.
static SlideState *new_slide_area(int width, int height) {
	GtkWidget *area = gtk_drawing_area_new();
	gtk_drawing_area_set_content_width(GTK_DRAWING_AREA(area), width);
	gtk_drawing_area_set_content_height(GTK_DRAWING_AREA(area), height);

	SlideState *s = calloc(1, sizeof(SlideState));
	s->area = area;
	gtk_drawing_area_set_draw_func(GTK_DRAWING_AREA(area), slide_draw, s, NULL);
	gtk_widget_add_tick_callback(area, slide_tick, s, NULL);
	return s;
}

// slide_retarget sets the transcript text and recomputes where it should
// sit within the content area: centered while it fits, otherwise
// right-aligned so the newest (rightmost) words stay in view and older
// ones slide off the left edge, clipped by draw_content. The very first
// call snaps instead of animating in.
static void slide_retarget(SlideState *s, int viewport_width, const char *text) {
	s->confirmation = FALSE;
	g_free(s->text);
	s->text = g_strdup(text);

	int text_w = 0;
	if (text[0] != '\0') {
		PangoLayout *layout = gtk_widget_create_pango_layout(s->area, text);
		pango_layout_set_single_paragraph_mode(layout, TRUE);
		pango_layout_get_pixel_size(layout, &text_w, NULL);
		g_object_unref(layout);
	}

	double cw = CONTENT_W(viewport_width);
	double target;
	if (text_w <= cw) {
		target = CONTENT_X + (cw - text_w) / 2.0;
	} else {
		target = CONTENT_X + cw - text_w;
	}
	s->target_x = target;

	if (!s->started) {
		s->started = TRUE;
		s->current_x = target;
	}
	gtk_widget_queue_draw(s->area);
}

// slide_show_confirmation switches the bar to the "copied" state. Cleared
// by the next slide_retarget -- Show() always calls it with "" first, so a
// fresh session never starts still showing a stale confirmation.
static void slide_show_confirmation(SlideState *s, const char *text) {
	g_free(s->text);
	s->text = g_strdup(text);
	s->confirmation = TRUE;
	s->level_target = 0;
	gtk_widget_queue_draw(s->area);
}

static void slide_set_level(SlideState *s, double level) {
	if (level < 0) {
		level = 0;
	} else if (level > 1) {
		level = 1;
	}
	s->level_target = level;
}

static void box_append(GtkWidget *box, GtkWidget *child) {
	gtk_box_append(GTK_BOX(box), child);
}

static void window_set_child(GtkWidget *window, GtkWidget *child) {
	gtk_window_set_child(GTK_WINDOW(window), child);
}

static void widget_set_visible(GtkWidget *widget, gboolean visible) {
	gtk_widget_set_visible(widget, visible);
}

static void widget_set_opacity(GtkWidget *widget, double opacity) {
	gtk_widget_set_opacity(widget, opacity);
}

static void widget_set_name(GtkWidget *widget, const char *name) {
	gtk_widget_set_name(widget, name);
}

static void load_css(const char *path) {
	GtkCssProvider *provider = gtk_css_provider_new();
	gtk_css_provider_load_from_path(provider, path);
	GdkDisplay *display = gdk_display_get_default();
	gtk_style_context_add_provider_for_display(display, GTK_STYLE_PROVIDER(provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
	g_object_unref(provider);
}

static void set_clipboard_text(const char *text) {
	GdkDisplay *display = gdk_display_get_default();
	GdkClipboard *clipboard = gdk_display_get_clipboard(display);
	gdk_clipboard_set_text(clipboard, text);
}

// prepare_overlay realizes window (creating its underlying X11 surface
// without mapping/showing it yet) and marks it override-redirect, so the
// window manager never adopts, decorates, or lists it. It does not
// position the window -- GTK doesn't finalize a window's real size
// synchronously (gtk_widget_get_width/height read 0x0 immediately after
// realize; the real size is only known after the main loop's first layout
// pass), and this session runs with X11/Wayland scale factor 3 (3840x2400
// physical output, confirmed via mutter's guard window at 1280x800), so
// GDK's own logical-pixel size accessors don't match the raw X11 geometry
// XMoveWindow needs anyway. See schedule_reposition, which centers the
// window shortly after it's shown by querying its *actual* raw X11
// geometry directly via Xlib rather than assuming a width.
static void prepare_overlay(GtkWidget *window, int width) {
	gtk_window_set_decorated(GTK_WINDOW(window), FALSE);
	gtk_window_set_default_size(GTK_WINDOW(window), width, -1);
	gtk_widget_realize(window);

	GtkNative *native = gtk_widget_get_native(window);
	GdkSurface *surface = gtk_native_get_surface(native);
	Window xid = gdk_x11_surface_get_xid(surface);
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);

	XSetWindowAttributes attrs;
	attrs.override_redirect = True;
	XChangeWindowAttributes(xdisplay, xid, CWOverrideRedirect, &attrs);
}

// ShowAnimState drives the intro animation: the panel fades in
// (gtk_widget_set_opacity, GTK's own render-tree alpha, independent of
// any X11/compositor-level opacity support) while the window eases
// upward into its final resting position from rise_px below it, both
// over duration_s, via a per-frame GtkTickCallback exactly like
// slide_tick -- see show_anim_tick.
typedef struct {
	Display *xdisplay;
	Window xid;
	GtkWidget *panel;
	int final_x, final_y;
	int rise_px;
	gint64 start_time;
	double duration_s;
} ShowAnimState;

static gboolean show_anim_tick(GtkWidget *widget, GdkFrameClock *clock, gpointer data) {
	ShowAnimState *s = (ShowAnimState *)data;

	gint64 now = gdk_frame_clock_get_frame_time(clock);
	double elapsed = (now - s->start_time) / 1000000.0;
	double t = elapsed / s->duration_s;
	if (t > 1.0) {
		t = 1.0;
	}
	double eased = 1.0 - pow(1.0 - t, 3.0); // ease-out cubic: fast start, gentle settle

	gtk_widget_set_opacity(s->panel, eased);

	int y = s->final_y + (int)round((1.0 - eased) * s->rise_px);
	XMoveWindow(s->xdisplay, s->xid, s->final_x, y);
	XFlush(s->xdisplay); // cheaper than XSync; no need to block for a round trip every frame

	if (t >= 1.0) {
		free(s);
		return G_SOURCE_REMOVE;
	}
	return G_SOURCE_CONTINUE;
}

typedef struct {
	GtkWidget *window;
	GtkWidget *panel;
	int bottom_margin;
	int rise_px;
	double duration_s;
} ShowAnimCtx;

static gboolean show_anim_start_cb(gpointer data) {
	ShowAnimCtx *ctx = (ShowAnimCtx *)data;

	GtkNative *native = gtk_widget_get_native(ctx->window);
	GdkSurface *surface = gtk_native_get_surface(native);
	Window xid = gdk_x11_surface_get_xid(surface);
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);

	XWindowAttributes real;
	XGetWindowAttributes(xdisplay, xid, &real);

	int screen = DefaultScreen(xdisplay);
	int screen_w = DisplayWidth(xdisplay, screen);
	int screen_h = DisplayHeight(xdisplay, screen);
	int x = (screen_w - real.width) / 2;
	if (x < 0) {
		x = 0;
	}
	int y = screen_h - real.height - ctx->bottom_margin;
	if (y < 0) {
		y = 0;
	}

	ShowAnimState *s = malloc(sizeof(ShowAnimState));
	s->xdisplay = xdisplay;
	s->xid = xid;
	s->panel = ctx->panel;
	s->final_x = x;
	s->final_y = y;
	s->rise_px = ctx->rise_px;
	s->start_time = gdk_frame_clock_get_frame_time(gtk_widget_get_frame_clock(ctx->window));
	s->duration_s = ctx->duration_s;

	gtk_widget_set_opacity(s->panel, 0.0);
	XMoveWindow(xdisplay, xid, x, y + s->rise_px);
	XSync(xdisplay, False);

	gtk_widget_add_tick_callback(ctx->window, show_anim_tick, s, NULL);

	free(ctx);
	return G_SOURCE_REMOVE;
}

// schedule_show_animation waits delay_ms (long enough for GTK to have
// finished its first real layout pass -- see prepare_overlay -- so the
// window's actual raw X11 size is known; needed for the same
// logical-vs-physical-pixel-scale reason documented there), then starts
// the fade+rise intro animation into its final centered,
// bottom_margin-above-the-bottom position.
static void schedule_show_animation(GtkWidget *window, GtkWidget *panel, int bottom_margin, guint delay_ms, int rise_px, double duration_s) {
	ShowAnimCtx *ctx = malloc(sizeof(ShowAnimCtx));
	ctx->window = window;
	ctx->panel = panel;
	ctx->bottom_margin = bottom_margin;
	ctx->rise_px = rise_px;
	ctx->duration_s = duration_s;
	g_timeout_add(delay_ms, show_anim_start_cb, ctx);
}

// HideAnimState drives the outro animation: the mirror image of
// ShowAnimState, fading the panel out while easing the window *down* by
// drop_px from wherever it currently sits, then actually hiding the
// window once done (see hide_anim_tick) -- so closing never just
// vanishes the window instantly either.
typedef struct {
	Display *xdisplay;
	Window xid;
	GtkWidget *window;
	GtkWidget *panel;
	int start_x, start_y;
	int drop_px;
	gint64 start_time;
	double duration_s;
} HideAnimState;

static gboolean hide_anim_tick(GtkWidget *widget, GdkFrameClock *clock, gpointer data) {
	HideAnimState *s = (HideAnimState *)data;

	gint64 now = gdk_frame_clock_get_frame_time(clock);
	double elapsed = (now - s->start_time) / 1000000.0;
	double t = elapsed / s->duration_s;
	if (t > 1.0) {
		t = 1.0;
	}
	double eased = pow(t, 2.0); // ease-in: gentle start, gathers pace -- reads as "dismissing"

	gtk_widget_set_opacity(s->panel, 1.0 - eased);

	int y = s->start_y + (int)round(eased * s->drop_px);
	XMoveWindow(s->xdisplay, s->xid, s->start_x, y);
	XFlush(s->xdisplay);

	if (t >= 1.0) {
		gtk_widget_set_visible(s->window, FALSE);
		free(s);
		return G_SOURCE_REMOVE;
	}
	return G_SOURCE_CONTINUE;
}

// start_hide_animation reads the window's actual current position (it
// only ever sits at its settled resting spot while visible, so no delayed
// "wait for real geometry" step is needed here, unlike the show side) and
// starts the fade+drop outro from there.
static void start_hide_animation(GtkWidget *window, GtkWidget *panel, int drop_px, double duration_s) {
	GtkNative *native = gtk_widget_get_native(window);
	GdkSurface *surface = gtk_native_get_surface(native);
	Window xid = gdk_x11_surface_get_xid(surface);
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);

	XWindowAttributes real;
	XGetWindowAttributes(xdisplay, xid, &real);

	HideAnimState *s = malloc(sizeof(HideAnimState));
	s->xdisplay = xdisplay;
	s->xid = xid;
	s->window = window;
	s->panel = panel;
	s->start_x = real.x;
	s->start_y = real.y;
	s->drop_px = drop_px;
	s->start_time = gdk_frame_clock_get_frame_time(gtk_widget_get_frame_clock(window));
	s->duration_s = duration_s;

	gtk_widget_add_tick_callback(window, hide_anim_tick, s, NULL);
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"runtime/cgo"
	"unsafe"
)

func init() {
	// GTK/GDK require every call to happen on one consistent OS thread for
	// the life of the program; lock the main goroutine to it before the Go
	// runtime can schedule it elsewhere. Must happen at init time, before
	// any other goroutine might run first (see Go's runtime.LockOSThread
	// docs).
	runtime.LockOSThread()

	// Force GDK's X11 backend (i.e. run through Xwayland) rather than
	// trying a native Wayland surface -- see the package doc comment for
	// why. Must be set before gtk_init_check.
	os.Setenv("GDK_BACKEND", "x11")
}

const (
	bottomMarginPx = 56
	// windowWidth is a floor, not the visual panel width: #zt-panel in
	// themes/*.css sits inset from the window edge by its own CSS margin,
	// so the panel's glow (a box-shadow, which GTK clips hard at the
	// window boundary) has room to fall off smoothly instead of being cut
	// off flush -- the window is expected to end up wider than this once
	// that margin is included in its natural size.
	windowWidth = 400
	// viewportWidthPx/viewportHeightPx size the clipping viewport the
	// transcript label slides around inside (see new_viewport /
	// slide_retarget): fixed regardless of text length, so the panel
	// itself never resizes as the transcript grows. These are logical
	// widget-space pixels (GtkFixed/measure coordinates), unrelated to the
	// raw X11 physical-pixel scale-factor issue documented above for
	// window positioning -- no conversion needed here. Tuned for the
	// default theme's font size and panel padding/margin. Height is tall
	// enough for the bigger idle "0type" wordmark (see slide_draw_idle),
	// not just the smaller transcript text -- both are vertically
	// centered within whatever height this is.
	viewportWidthPx  = 300
	viewportHeightPx = 30
	// repositionDelayMs must exceed how long GTK takes to finish its
	// first real layout pass after being shown; measured at ~well under
	// 500ms during development, so 150ms leaves comfortable margin
	// without being a noticeable visible delay/jump.
	repositionDelayMs = 150
	// showAnimRisePx/showAnimDurationS shape the intro animation Show
	// plays once the window's real size is known (see
	// schedule_show_animation): it fades in while easing up into its
	// final resting position from this many pixels below it.
	showAnimRisePx    = 18
	showAnimDurationS = 0.32
)

// Window is 0type's floating overlay: a small panel positioned near the
// bottom-center of the screen, override-redirect (no window manager
// decoration, no taskbar/alt-tab entry).
type Window struct {
	win   *C.GtkWidget
	panel *C.GtkWidget
	slide *C.SlideState
	loop  *C.GMainLoop
}

// New creates the window and builds its widget tree: a styled panel
// containing a fixed-size drawing area that the transcript text slides
// around inside as it comes in (see SetText). Pass "" for initialText to
// start idle (a pulsing dot; see slide_draw) rather than with text
// already showing. It must be called from the same goroutine that will
// later call Run.
func New(initialText string) (*Window, error) {
	if C.gtk_init_check() == C.FALSE {
		return nil, fmt.Errorf("ui: gtk_init_check failed (no display? is Xwayland available?)")
	}

	win := C.new_window()

	box := C.new_box_vertical()
	withCString("zt-panel", func(c *C.char) { C.widget_set_name(box, c) })

	slide := C.new_slide_area(viewportWidthPx, viewportHeightPx)
	withCString("zt-label", func(c *C.char) { C.widget_set_name(slide.area, c) })

	C.box_append(box, slide.area)
	C.window_set_child(win, box)

	withCString(initialText, func(c *C.char) { C.slide_retarget(slide, viewportWidthPx, c) })

	return &Window{win: win, panel: box, slide: slide}, nil
}

// LoadCSS applies the stylesheet at path application-wide. Selectors
// #zt-panel and #zt-label target this window's widgets (see New).
func (w *Window) LoadCSS(path string) {
	withCString(path, func(c *C.char) { C.load_css(c) })
}

// SetText updates the transcript text and smoothly slides it into its new
// position: centered while it fits the display, otherwise sliding left so
// the newest words stay visible and older ones scroll off the left edge
// -- the sentence visibly builds up toward its final form, and the panel
// never resizes or jumps (see slide_tick/slide_retarget). Must be called
// from the GTK main thread -- use RunOnMainThread from any other
// goroutine.
func (w *Window) SetText(text string) {
	withCString(text, func(c *C.char) { C.slide_retarget(w.slide, viewportWidthPx, c) })
}

// SetClipboard replaces the system clipboard's contents with text, so the
// user can paste it elsewhere once they're done dictating. Must be called
// from the GTK main thread -- use RunOnMainThread from any other
// goroutine.
func SetClipboard(text string) {
	withCString(text, func(c *C.char) { C.set_clipboard_text(c) })
}

// SetLevel feeds the bar's live level meter (0..1, from the capture
// pipeline's per-chunk dBFS). Must be called from the GTK main thread --
// use RunOnMainThread from any other goroutine.
func (w *Window) SetLevel(level float64) {
	C.slide_set_level(w.slide, C.double(level))
}

// ShowCopiedConfirmation replaces the display with a brief "copied"
// status message (solid accent green, centered) in place of the
// transcript, until the next SetText call (Show always makes one, with
// "", so a fresh session never starts still showing a stale
// confirmation). Must be called from the GTK main thread -- use
// RunOnMainThread from any other goroutine.
func (w *Window) ShowCopiedConfirmation() {
	withCString("Copied to clipboard", func(c *C.char) { C.slide_show_confirmation(w.slide, c) })
}

// Show makes the window visible and plays its intro animation (fade in
// while easing up into its final resting position; see
// schedule_show_animation). Must be called from the GTK main thread --
// use RunOnMainThread from any other goroutine.
func (w *Window) Show() {
	// Opacity must already be 0 *before* the window becomes visible, not
	// only later once schedule_show_animation's delayed callback gets
	// around to it -- otherwise the window flashes in at full opacity
	// (wherever it was last positioned) for the whole repositionDelayMs
	// wait, then jumps to the animation's start state. That was the bug
	// behind the intro "not being seen properly": there was a real,
	// visible flash-then-jump before any fade/rise ever started.
	C.widget_set_opacity(w.panel, 0.0)
	C.widget_set_visible(w.win, C.TRUE)
	C.schedule_show_animation(w.win, w.panel, C.int(bottomMarginPx), repositionDelayMs, showAnimRisePx, showAnimDurationS)
}

// Hide plays the outro animation (fade out while easing down by
// showAnimRisePx -- the mirror of Show's intro) and makes the window
// invisible once it completes. Must be called from the GTK main thread --
// use RunOnMainThread from any other goroutine.
func (w *Window) Hide() {
	C.start_hide_animation(w.win, w.panel, showAnimRisePx, showAnimDurationS)
}

// RunOnMainThread schedules fn to run on the GTK main loop thread as soon
// as it's next idle, and returns immediately without waiting for it to
// run. Safe to call from any goroutine -- this is how 0type's audio/ASR
// pipeline (running on its own goroutines) delivers transcript updates to
// the UI.
func RunOnMainThread(fn func()) {
	h := cgo.NewHandle(func() bool {
		fn()
		return false // G_SOURCE_REMOVE: run once
	})
	C.schedule_idle_source(C.guintptr(h))
}

// Run prepares the window (realized, override-redirect, not yet shown --
// use Show/Hide to control visibility, e.g. from internal/toggle) and
// blocks running the GTK main loop until it's asked to quit (via Quit, or
// SIGINT/SIGTERM).
func (w *Window) Run() {
	C.prepare_overlay(w.win, C.int(windowWidth))

	w.loop = C.g_main_loop_new(nil, C.FALSE)
	installQuitSignal := func(signum C.int) {
		h := cgo.NewHandle(func() bool {
			C.g_main_loop_quit(w.loop)
			return false // G_SOURCE_REMOVE
		})
		C.schedule_unix_signal(signum, C.guintptr(h))
	}
	installQuitSignal(C.SIGINT)
	installQuitSignal(C.SIGTERM)

	C.g_main_loop_run(w.loop)
}

// Quit stops the main loop started by Run. Safe to call from any
// goroutine (it schedules the actual quit onto the main thread).
func (w *Window) Quit() {
	RunOnMainThread(func() {
		if w.loop != nil {
			C.g_main_loop_quit(w.loop)
		}
	})
}

//export goSourceTrampoline
func goSourceTrampoline(handle C.guintptr) C.gboolean {
	h := cgo.Handle(handle)
	fn := h.Value().(func() bool)
	keep := fn()
	if !keep {
		h.Delete()
		return C.FALSE
	}
	return C.TRUE
}

// withCString converts s to a C string, passes it to fn, and frees it
// afterward -- a small helper so call sites (all one-shot setup calls, not
// hot paths) don't repeat the CString/free dance.
func withCString(s string, fn func(*C.char)) {
	c := C.CString(s)
	defer C.free(unsafe.Pointer(c))
	fn(c)
}

// withCStringRet is withCString for calls that return a value.
func withCStringRet(s string, fn func(*C.char) *C.GtkWidget) *C.GtkWidget {
	c := C.CString(s)
	defer C.free(unsafe.Pointer(c))
	return fn(c)
}
