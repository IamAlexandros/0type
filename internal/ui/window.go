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

// SlideState is the transcript display. It's a GtkDrawingArea, not a
// GtkLabel -- deliberately: every attempt to get a fixed-size *container*
// (GtkFixed, then GtkScrolledWindow) to hold a growing label without its
// own reported size leaking the label's length up into the rest of the
// panel ran into some variant of the same trap (a child's minimum/natural
// size bubbling through regardless of size_request or propagate-natural
// settings). A GtkDrawingArea sidesteps the whole problem: its size is
// simply whatever gtk_drawing_area_set_content_width/height says, always,
// because it has no children for anything to leak from -- text is painted
// directly with Cairo/Pango in slide_draw, at whatever x offset
// slide_tick's animation currently computes. Text is never wrapped or
// ellipsized; the drawing area's own bounds are what hide the overflow.
typedef struct {
	GtkWidget *area;
	char *text;
	double current_x;
	double target_x;
	gint64 last_time;
	gboolean started;
} SlideState;

static void slide_draw(GtkDrawingArea *area, cairo_t *cr, int width, int height, gpointer data) {
	SlideState *s = (SlideState *)data;
	if (s->text == NULL || s->text[0] == '\0') {
		return;
	}

	PangoLayout *layout = gtk_widget_create_pango_layout(GTK_WIDGET(area), s->text);
	pango_layout_set_single_paragraph_mode(layout, TRUE);

	int text_h;
	pango_layout_get_pixel_size(layout, NULL, &text_h);

	GdkRGBA color;
	gtk_widget_get_color(GTK_WIDGET(area), &color);
	gdk_cairo_set_source_rgba(cr, &color);

	cairo_move_to(cr, s->current_x, (height - text_h) / 2.0);
	pango_cairo_show_layout(cr, layout);

	g_object_unref(layout);
}

// slide_tick advances current_x toward target_x by a fraction of the
// remaining distance each frame, scaled by actual elapsed time (not an
// assumed frame rate) via a simple exponential ease -- this is what makes
// the motion genuinely smooth (frame-clock synced, not an instant snap)
// and automatically continuous even if target_x changes again before the
// previous move finishes (see slide_retarget).
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
		const double tau = 0.11; // seconds; smaller = snappier, larger = lazier
		double factor = 1.0 - exp(-dt / tau);
		s->current_x += diff * factor;
	}
	gtk_widget_queue_draw(s->area);
	return G_SOURCE_CONTINUE;
}

// new_slide_area creates the fixed-size transcript display and its
// animation state together, and starts the per-frame tick callback that
// drives it for the window's lifetime.
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

// slide_retarget sets the text to display and recomputes where it should
// sit: centered while it fits within viewport_width, otherwise
// right-aligned so the newest (rightmost) text stays in view and older
// text slides off the left edge, clipped by the drawing area's own
// bounds. The very first call snaps instead of animating in, so the
// initial text doesn't slide in from nowhere.
static void slide_retarget(SlideState *s, int viewport_width, const char *text) {
	g_free(s->text);
	s->text = g_strdup(text);

	int text_w = 0;
	if (text[0] != '\0') {
		PangoLayout *layout = gtk_widget_create_pango_layout(s->area, text);
		pango_layout_set_single_paragraph_mode(layout, TRUE);
		pango_layout_get_pixel_size(layout, &text_w, NULL);
		g_object_unref(layout);
	}

	double target;
	if (text_w <= viewport_width) {
		target = (viewport_width - text_w) / 2.0;
	} else {
		target = (double)(viewport_width - text_w);
	}
	s->target_x = target;

	if (!s->started) {
		s->started = TRUE;
		s->current_x = target;
	}
	gtk_widget_queue_draw(s->area);
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

typedef struct {
	GtkWidget *window;
	int bottom_margin;
} RepositionCtx;

static gboolean reposition_cb(gpointer data) {
	RepositionCtx *ctx = (RepositionCtx *)data;

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
	XMoveWindow(xdisplay, xid, x, y);
	XSync(xdisplay, False);

	free(ctx);
	return G_SOURCE_REMOVE;
}

// schedule_reposition centers window horizontally and anchors it
// bottom_margin pixels above the bottom of the screen, delay_ms after
// being called -- long enough for GTK to have finished its first real
// layout pass (see prepare_overlay) so the window's actual raw X11 size is
// known, avoiding the logical-vs-physical-pixel mismatch a scaled session
// would otherwise hit if we tried to compute this synchronously.
static void schedule_reposition(GtkWidget *window, int bottom_margin, guint delay_ms) {
	RepositionCtx *ctx = malloc(sizeof(RepositionCtx));
	ctx->window = window;
	ctx->bottom_margin = bottom_margin;
	g_timeout_add(delay_ms, reposition_cb, ctx);
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
	// default theme's font size and panel padding/margin.
	viewportWidthPx  = 260
	viewportHeightPx = 22
	// repositionDelayMs must exceed how long GTK takes to finish its
	// first real layout pass after being shown; measured at ~well under
	// 500ms during development, so 150ms leaves comfortable margin
	// without being a noticeable visible delay/jump.
	repositionDelayMs = 150
)

// Window is 0type's floating overlay: a small panel positioned near the
// bottom-center of the screen, override-redirect (no window manager
// decoration, no taskbar/alt-tab entry).
type Window struct {
	win   *C.GtkWidget
	slide *C.SlideState
	loop  *C.GMainLoop
}

// New creates the window and builds its widget tree: a styled panel
// containing a fixed-size drawing area that the transcript text slides
// around inside as it comes in (see SetText). It must be called from the
// same goroutine that will later call Run.
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

	return &Window{win: win, slide: slide}, nil
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

// Show makes the window visible and (re-)positions it. Must be called
// from the GTK main thread -- use RunOnMainThread from any other
// goroutine.
func (w *Window) Show() {
	C.widget_set_visible(w.win, C.TRUE)
	C.schedule_reposition(w.win, C.int(bottomMarginPx), repositionDelayMs)
}

// Hide makes the window invisible. Must be called from the GTK main
// thread -- use RunOnMainThread from any other goroutine.
func (w *Window) Hide() {
	C.widget_set_visible(w.win, C.FALSE)
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
