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
#include <string.h>
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

// goKeyPressed is exported below; key_pressed_c adapts it to GTK's
// key-pressed signal signature. Returning TRUE stops the event from
// propagating further, which is what we want for any key the menu
// actually handles.
extern gboolean goKeyPressed(guint keyval);

static gboolean key_pressed_c(GtkEventControllerKey *controller, guint keyval, guint keycode, GdkModifierType state, gpointer data) {
	return goKeyPressed(keyval);
}

// install_key_controller puts the key handler on the window in the
// *capture* phase. The default bubble phase propagates up from whatever
// widget holds focus, and this window deliberately contains nothing
// focusable (a drawing area and some invisible probes), so in bubble
// phase there is no target for an event to start from and key presses are
// simply dropped. Capture runs top-down from the toplevel instead, which
// needs no focus widget at all.
static void install_key_controller(GtkWidget *window) {
	GtkEventController *controller = gtk_event_controller_key_new();
	gtk_event_controller_set_propagation_phase(controller, GTK_PHASE_CAPTURE);
	g_signal_connect(controller, "key-pressed", G_CALLBACK(key_pressed_c), NULL);
	gtk_widget_add_controller(window, controller);
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

// The largest sprite a theme may draw for itself. 16x16 is already more
// resolution than the 22px tile can show crisply -- past that the cells
// stop landing on whole device pixels, which is the one thing pixel art
// cannot survive.
#define ART_MAX 16

// The overlay draws one of two things, in the same panel: the dictation
// bar, or a menu.
#define MODE_BAR  0
#define MODE_MENU 1

// Menu geometry, in the drawing area's logical pixels. MENU_ROW_H is
// deliberately close to the bar's own height so the panel doesn't change
// character between modes.
#define MENU_MAX_ITEMS 16
#define MENU_ROW_H     30.0
#define MENU_ROW_PAD    6.0
#define MENU_RADIUS     8.0

typedef struct {
	char *label;
	char *detail; // right-aligned secondary text (the current value, a hint)
} MenuItem;

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

	// Color probes: permanently invisible widgets that exist only to carry
	// a CSS `color` the theme can set (see new_color_probe). The bar's
	// content is drawn with Cairo, which knows nothing about CSS, so
	// without these every color here would be a constant compiled into the
	// binary and a "theme" could only restyle the panel behind it. Reading
	// them per-draw (rather than caching at startup) means a theme loaded
	// later still takes effect.
	GtkWidget *probe_accent;  // brand mark
	GtkWidget *probe_muted;   // idle placeholder, and the transcript's fade tail
	GtkWidget *probe_success; // "copied" confirmation
	GtkWidget *probe_meter;   // level bars
	GtkWidget *probe_tile;    // the mark's tile surface

	int mark;         // which brand mark to draw (MARK_MIC/MARK_PIXEL/MARK_ZERO)
	char *idle_text;  // themeable placeholder shown when there's nothing to say

	// A theme may supply its own sprite instead of picking a built-in
	// mark (see slide_set_art). art_rows == 0 means it hasn't.
	char art[ART_MAX][ART_MAX + 1];
	int art_rows, art_cols;

	// Menu mode. The same drawing area renders either the dictation bar or
	// a menu, rather than there being a second window: the panel's look,
	// position, intro/outro animation and theming are all attached to this
	// one window, and a separate menu window would have to reimplement
	// every one of them to look like it belonged to the same program.
	int mode;
	MenuItem menu[MENU_MAX_ITEMS];
	int menu_count;
	int menu_selected;
	double sel_y_current; // eased toward the selected row, so the highlight glides
	double sel_y_target;
	gboolean sel_started;
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

// The brand mark the bar opens with. Which one is drawn is a *theme*
// decision (see internal/theme's mark directive), not a build-time one,
// because a mark that suits one theme can look wrong in another -- the
// smooth vector mic that fits the default pill is exactly the thing that
// breaks the illusion in the pixel-art terminal theme.
//
// A typed "0" was tried before any of these and rejected: in a
// proportional face it's a plain oval that reads as the letter O, and
// even a mono face's dotted zero read as a number badge, not a logo.
#define MARK_MIC         0
#define MARK_PIXEL       1
#define MARK_ZERO        2
// IDLE_TEXT_DEFAULT is only a fallback; the wording is a theme's choice
// (see internal/theme's idle directive), because a pastiche that keeps
// saying "Listening…" in someone else's voice isn't much of a pastiche.
#define IDLE_TEXT_DEFAULT "Listening…"
#define FADE_W           40.0

// The pixel-art mark is a bitmap rather than a scaled-down vector: 8-bit
// means visible, square, aligned pixels, which is precisely what you lose
// by shrinking a smooth path. Cells are drawn at PIXEL_CELL logical
// pixels, chosen so the grid lands on whole device pixels at this
// display's scale factor and the edges stay hard.
#define PIXEL_CELL 2.0
#define PIXEL_COLS 7
#define PIXEL_ROWS 9



// The cradle arms are what make this read as a microphone rather than a
// pawn or a nail, so they run *alongside* the head rather than below it,
// and the stand narrows on the way down (cradle, stem, foot) instead of
// repeating the cradle's width -- two equal bars with a stem between them
// read as furniture, which earlier drafts of this sprite duly did.
static const char *const MARK_PIXEL_MIC[PIXEL_ROWS] = {
	"..###..", // head
	"..###..",
	"#.###.#", // cradle arms, flanking the head
	"#.###.#",
	"#.###.#",
	"#.###.#",
	".#####.", // cradle
	"...#...", // stem
	"..###..", // foot
};

static const char *const MARK_PIXEL_CHECK[PIXEL_ROWS] = {
	".......",
	".......",
	"......#",
	".....##",
	"#...##.",
	"##.##..",
	".####..",
	"..##...",
	".......",
};

// set_probe_color makes a color probe's themed CSS color the current Cairo
// source, scaling its alpha by `alpha` (1.0 = exactly as the theme set it).
static void set_probe_color(cairo_t *cr, GtkWidget *probe, double alpha) {
	GdkRGBA c;
	gtk_widget_get_color(probe, &c);
	cairo_set_source_rgba(cr, c.red, c.green, c.blue, c.alpha * alpha);
}

static void rounded_rect(cairo_t *cr, double x, double y, double w, double h, double r) {
	if (r <= 0) {
		cairo_rectangle(cr, x, y, w, h); // the pixel mark's tile has hard corners
		return;
	}
	cairo_new_sub_path(cr);
	cairo_arc(cr, x + w - r, y + r, r, -G_PI / 2, 0);
	cairo_arc(cr, x + w - r, y + h - r, r, 0, G_PI / 2);
	cairo_arc(cr, x + r, y + h - r, r, G_PI / 2, G_PI);
	cairo_arc(cr, x + r, y + r, r, G_PI, 3 * G_PI / 2);
	cairo_close_path(cr);
}

// draw_mark paints the brand tile at the left edge: a subtle rounded
// square holding the accent mark (MARK_STYLE) -- or, during the
// confirmation, a green check, so the whole bar reads as "done" at a glance.
// draw_bitmap paints a grid of '#' cells centered in the tile, snapped to
// whole logical pixels. cell is chosen by the caller so the whole sprite
// fits the tile; cells stay square and integer-sized, because a fractional
// cell is an antialiased edge and an antialiased edge is not pixel art.
static void draw_bitmap(cairo_t *cr, const char *rows, int nrows, int ncols, int stride, double cell, double tile_y) {
	double art_w = ncols * cell, art_h = nrows * cell;
	double x0 = floor((MARK_SIZE - art_w) / 2.0);
	double y0 = floor(tile_y + (MARK_SIZE - art_h) / 2.0);
	for (int r = 0; r < nrows; r++) {
		for (int c = 0; c < ncols; c++) {
			if (rows[r * stride + c] != '#') {
				continue;
			}
			cairo_rectangle(cr, x0 + c * cell, y0 + r * cell, cell, cell);
		}
	}
	cairo_fill(cr);
}

// draw_pixel_art paints one of the built-in 7x9 bitmaps above.
static void draw_pixel_art(cairo_t *cr, const char *const *rows, double tile_y) {
	char flat[PIXEL_ROWS * PIXEL_COLS];
	for (int r = 0; r < PIXEL_ROWS; r++) {
		memcpy(flat + r * PIXEL_COLS, rows[r], PIXEL_COLS);
	}
	draw_bitmap(cr, flat, PIXEL_ROWS, PIXEL_COLS, PIXEL_COLS, PIXEL_CELL, tile_y);
}

// art_cell picks the largest whole-pixel cell size that fits the sprite
// inside the tile.
static double art_cell(int rows, int cols) {
	int longest = rows > cols ? rows : cols;
	double cell = floor(18.0 / longest);
	return cell < 1.0 ? 1.0 : cell;
}

static void draw_mark(GtkWidget *area, cairo_t *cr, int height, SlideState *s) {
	gboolean confirmed = s->confirmation;
	// A theme's own sprite is pixel art by construction, so it gets the
	// same hard-cornered tile the built-in pixel mark does.
	gboolean pixel = (s->mark == MARK_PIXEL) || (s->art_rows > 0);
	double y = (height - MARK_SIZE) / 2.0;

	// The tile is a flat surface while idle, and picks up the success tint
	// during the confirmation so the whole left edge reads as "done". Its
	// corners follow the mark: rounding a tile around pixel art
	// reintroduces exactly the smooth curve the art is avoiding.
	double radius = pixel ? 0.0 : MARK_RADIUS;
	set_probe_color(cr, confirmed ? s->probe_success : s->probe_tile, confirmed ? 0.18 : 1.0);
	rounded_rect(cr, 0.5, y + 0.5, MARK_SIZE - 1, MARK_SIZE - 1, radius);
	cairo_fill_preserve(cr);
	set_probe_color(cr, s->probe_tile, 1.35); // hairline: the same surface color, slightly stronger
	cairo_set_line_width(cr, 1.0);
	cairo_stroke(cr);

	if (confirmed) {
		set_probe_color(cr, s->probe_success, 1.0);
		if (pixel) {
			draw_pixel_art(cr, MARK_PIXEL_CHECK, y);
			return;
		}
		PangoLayout *layout = gtk_widget_create_pango_layout(area, "✓");
		int tw, th;
		pango_layout_get_pixel_size(layout, &tw, &th);
		cairo_move_to(cr, (MARK_SIZE - tw) / 2.0, y + (MARK_SIZE - th) / 2.0);
		pango_cairo_show_layout(cr, layout);
		g_object_unref(layout);
		return;
	}

	// The mark itself is drawn, not typed: a font glyph dropped into a
	// tile read as a number badge, not a logo.
	double cx = MARK_SIZE / 2.0, cy = y + MARK_SIZE / 2.0;
	set_probe_color(cr, s->probe_accent, 1.0);
	cairo_set_line_cap(cr, CAIRO_LINE_CAP_ROUND);

	if (s->art_rows > 0) {
		draw_bitmap(cr, &s->art[0][0], s->art_rows, s->art_cols, ART_MAX + 1,
		            art_cell(s->art_rows, s->art_cols), y);
		return;
	}

	if (pixel) {
		draw_pixel_art(cr, MARK_PIXEL_MIC, y);
		return;
	}

	if (s->mark == MARK_ZERO) {
		// Geometric zero: a stroked capsule ring with a center dot -- the
		// "dotted zero" idea, as an icon.
		double rw = 8.0, rh = 12.0;
		cairo_set_line_width(cr, 1.75);
		rounded_rect(cr, cx - rw / 2, cy - rh / 2, rw, rh, rw / 2);
		cairo_stroke(cr);
		cairo_arc(cr, cx, cy, 1.3, 0, 2 * G_PI);
		cairo_fill(cr);
		return;
	}

	// Mic: capsule body, U cradle, stem, base.
	double bw = 5.5, bh = 9.5;
	rounded_rect(cr, cx - bw / 2, cy - 7.0, bw, bh, bw / 2);
	cairo_fill(cr);
	cairo_set_line_width(cr, 1.5);
	cairo_new_sub_path(cr);
	cairo_arc(cr, cx, cy - 0.5, 5.0, 0, G_PI);
	cairo_stroke(cr);
	cairo_move_to(cr, cx, cy + 4.5);
	cairo_line_to(cr, cx, cy + 7.0);
	cairo_stroke(cr);
	cairo_move_to(cr, cx - 3.0, cy + 7.0);
	cairo_line_to(cr, cx + 3.0, cy + 7.0);
	cairo_stroke(cr);
}

// draw_bars paints the live level meter at the right edge: three thin
// bars whose heights follow the (eased) mic level. Muted, so it's an
// affordance that the bar is listening, not a feature.
static void draw_bars(cairo_t *cr, int width, int height, SlideState *s) {
	double level = s->level_current;
	const double min_h = 6.0, max_h = 16.0;
	// The middle bar leads and the outer two lag slightly, so it reads as
	// a signal rather than three identical sliders.
	const double scale[BAR_COUNT] = {0.7, 1.0, 0.85};
	double x0 = width - BARS_W;
	for (int i = 0; i < BAR_COUNT; i++) {
		double h = min_h + (max_h - min_h) * level * scale[i];
		double x = x0 + i * (BAR_W + BAR_GAP);
		double y = (height - h) / 2.0;
		// Louder input doesn't just raise the bars, it brightens them --
		// the meter reads as responding even at a glance.
		set_probe_color(cr, s->probe_meter, 0.22 + 0.35 * level);
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
	const char *text = idle ? (s->idle_text ? s->idle_text : IDLE_TEXT_DEFAULT) : s->text;
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
		set_probe_color(cr, s->probe_muted, 1.0);
		cairo_move_to(cr, CONTENT_X + (cw - tw) / 2.0, y);
	} else if (s->confirmation) {
		set_probe_color(cr, s->probe_success, 1.0);
		cairo_move_to(cr, CONTENT_X + (cw - tw) / 2.0, y);
	} else {
		GdkRGBA color, fade;
		gtk_widget_get_color(area, &color);  // #zt-label: the transcript's own color
		gtk_widget_get_color(s->probe_muted, &fade);
		cairo_pattern_t *g = cairo_pattern_create_linear(CONTENT_X, 0, CONTENT_X + FADE_W, 0);
		cairo_pattern_add_color_stop_rgba(g, 0.0, fade.red, fade.green, fade.blue, color.alpha * 0.45);
		cairo_pattern_add_color_stop_rgba(g, 1.0, color.red, color.green, color.blue, color.alpha);
		cairo_set_source(cr, g);
		cairo_pattern_destroy(g); // cairo_set_source holds its own reference
		cairo_move_to(cr, s->current_x, y);
	}
	pango_cairo_show_layout(cr, layout);
	g_object_unref(layout);
	cairo_restore(cr);
}

// draw_menu paints the menu: one row per item, with the selected row
// carrying a soft highlight that glides between rows rather than
// teleporting (see slide_tick). Labels use the panel's text color and
// details the muted one, so a menu inherits a theme without the theme
// having to know menus exist.
static void draw_menu(GtkWidget *area, cairo_t *cr, int width, int height, SlideState *s) {
	double x = MENU_ROW_PAD, w = width - 2 * MENU_ROW_PAD;

	if (s->menu_count > 0) {
		set_probe_color(cr, s->probe_accent, 0.16);
		rounded_rect(cr, x, s->sel_y_current + 2, w, MENU_ROW_H - 4, MENU_RADIUS);
		cairo_fill(cr);
	}

	GdkRGBA text;
	gtk_widget_get_color(area, &text);

	for (int i = 0; i < s->menu_count; i++) {
		double row_y = i * MENU_ROW_H;
		gboolean selected = (i == s->menu_selected);

		PangoLayout *layout = gtk_widget_create_pango_layout(area, s->menu[i].label);
		pango_layout_set_single_paragraph_mode(layout, TRUE);
		int tw, th;
		pango_layout_get_pixel_size(layout, &tw, &th);
		// The selected row's label takes the accent color; everything else
		// stays text-colored, so the eye lands on one row.
		if (selected) {
			set_probe_color(cr, s->probe_accent, 1.0);
		} else {
			cairo_set_source_rgba(cr, text.red, text.green, text.blue, text.alpha * 0.82);
		}
		cairo_move_to(cr, x + MENU_ROW_PAD + 4, row_y + (MENU_ROW_H - th) / 2.0);
		pango_cairo_show_layout(cr, layout);
		g_object_unref(layout);

		if (s->menu[i].detail == NULL || s->menu[i].detail[0] == '\0') {
			continue;
		}
		PangoLayout *dl = gtk_widget_create_pango_layout(area, s->menu[i].detail);
		pango_layout_set_single_paragraph_mode(dl, TRUE);
		int dw, dh;
		pango_layout_get_pixel_size(dl, &dw, &dh);
		set_probe_color(cr, s->probe_muted, 1.0);
		cairo_move_to(cr, x + w - MENU_ROW_PAD - 4 - dw, row_y + (MENU_ROW_H - dh) / 2.0);
		pango_cairo_show_layout(cr, dl);
		g_object_unref(dl);
	}
}

static void slide_draw(GtkDrawingArea *area, cairo_t *cr, int width, int height, gpointer data) {
	SlideState *s = (SlideState *)data;
	if (s->mode == MODE_MENU) {
		draw_menu(GTK_WIDGET(area), cr, width, height, s);
		return;
	}
	draw_content(GTK_WIDGET(area), cr, width, height, s);
	draw_mark(GTK_WIDGET(area), cr, height, s);
	draw_bars(cr, width, height, s);
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

	double sel_diff = s->sel_y_target - s->sel_y_current;
	if (fabs(sel_diff) < 0.25) {
		s->sel_y_current = s->sel_y_target;
	} else {
		s->sel_y_current += sel_diff * (1.0 - exp(-dt / 0.07)); // faster than the text: a cursor should feel immediate
	}

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

static void menu_clear(SlideState *s) {
	for (int i = 0; i < s->menu_count; i++) {
		g_free(s->menu[i].label);
		g_free(s->menu[i].detail);
		s->menu[i].label = NULL;
		s->menu[i].detail = NULL;
	}
	s->menu_count = 0;
}

static void menu_add(SlideState *s, const char *label, const char *detail) {
	if (s->menu_count >= MENU_MAX_ITEMS) {
		return;
	}
	s->menu[s->menu_count].label = g_strdup(label);
	s->menu[s->menu_count].detail = g_strdup(detail);
	s->menu_count++;
}

static void menu_set_selected(SlideState *s, int selected) {
	if (selected < 0 || selected >= s->menu_count) {
		return;
	}
	s->menu_selected = selected;
	s->sel_y_target = selected * MENU_ROW_H;
	if (!s->sel_started) {
		s->sel_started = TRUE;
		s->sel_y_current = s->sel_y_target; // the first selection snaps; later ones glide
	}
	gtk_widget_queue_draw(s->area);
}

// resize_to_content forces the toplevel to the size its content now
// wants. Growing the drawing area alone is not enough: GTK settles a
// toplevel's size when it is mapped and then leaves it there, so a window
// that was mapped as a one-line bar stays one line tall no matter how
// much taller its child asks to be -- the extra rows just draw outside
// the panel. Measuring the panel (gtk_widget_measure includes its CSS
// margin, border and padding) and setting that as the default size is
// what actually moves the window.
static void resize_to_content(GtkWidget *window, GtkWidget *panel) {
	int min_w, nat_w, min_h, nat_h;
	gtk_widget_measure(panel, GTK_ORIENTATION_HORIZONTAL, -1, &min_w, &nat_w, NULL, NULL);
	gtk_widget_measure(panel, GTK_ORIENTATION_VERTICAL, nat_w, &min_h, &nat_h, NULL, NULL);
	gtk_window_set_default_size(GTK_WINDOW(window), nat_w, nat_h);
	gtk_widget_queue_resize(window);

	// The default size alone doesn't move an already-realized window: the
	// surface was sized when prepare_overlay realized it, back when the
	// panel still held a one-line bar, and GTK won't renegotiate a
	// toplevel it has already given a surface. This window's geometry is
	// driven directly through Xlib anyway (see prepare_overlay and
	// show_anim_start_cb, which move it in raw screen coordinates), so
	// resize it the same way -- GDK picks the new size up from the
	// resulting ConfigureNotify and GTK reallocates the panel to match.
	GtkNative *native = gtk_widget_get_native(window);
	GdkSurface *surface = native ? gtk_native_get_surface(native) : NULL;
	if (!surface) {
		return;
	}
	int scale = gdk_surface_get_scale_factor(surface);
	if (scale < 1) {
		scale = 1;
	}
	Window xid = gdk_x11_surface_get_xid(surface);
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);
	XResizeWindow(xdisplay, xid, (unsigned)(nat_w * scale), (unsigned)(nat_h * scale));
	XFlush(xdisplay);
}

// menu_commit switches the panel into menu mode, sizes the drawing area
// to fit the rows, and resizes the window around it. Show reads the
// window's real geometry when it positions it, so nothing else has to
// know the height changed.
static void menu_commit(SlideState *s, GtkWidget *window, GtkWidget *panel, int width, int selected) {
	s->mode = MODE_MENU;
	s->sel_started = FALSE;
	menu_set_selected(s, selected);
	gtk_drawing_area_set_content_width(GTK_DRAWING_AREA(s->area), width);
	gtk_drawing_area_set_content_height(GTK_DRAWING_AREA(s->area), (int)(s->menu_count * MENU_ROW_H));
	resize_to_content(window, panel);
}

// menu_dismiss returns the panel to the dictation bar at its usual size.
static void menu_dismiss(SlideState *s, GtkWidget *window, GtkWidget *panel, int width, int height) {
	menu_clear(s);
	s->mode = MODE_BAR;
	gtk_drawing_area_set_content_width(GTK_DRAWING_AREA(s->area), width);
	gtk_drawing_area_set_content_height(GTK_DRAWING_AREA(s->area), height);
	resize_to_content(window, panel);
}

static void slide_set_idle_text(SlideState *s, const char *text) {
	g_free(s->idle_text);
	s->idle_text = g_strdup(text);
	gtk_widget_queue_draw(s->area);
}

// slide_set_art installs a theme-supplied sprite, given as rows
// concatenated into one string. Passing 0 rows clears it and returns the
// bar to whichever built-in mark is selected.
static void slide_set_art(SlideState *s, const char *flat, int rows, int cols) {
	s->art_rows = 0;
	s->art_cols = 0;
	if (rows > 0 && rows <= ART_MAX && cols > 0 && cols <= ART_MAX) {
		for (int r = 0; r < rows; r++) {
			memcpy(s->art[r], flat + r * cols, cols);
			s->art[r][cols] = '\0';
		}
		s->art_rows = rows;
		s->art_cols = cols;
	}
	gtk_widget_queue_draw(s->area);
}

static void slide_set_mark(SlideState *s, int mark) {
	s->mark = mark;
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

// new_color_probe adds an invisible label to the panel whose only purpose
// is to be a CSS selector target: the theme sets `color` on #<name>, and
// the Cairo drawing code reads it back with gtk_widget_get_color (see
// set_probe_color). Invisible children are skipped during layout, so a
// probe costs nothing visually, and the widget must be in the window's
// hierarchy (not free-floating) for GTK to compute its style at all.
static GtkWidget *new_color_probe(GtkWidget *box, const char *name) {
	GtkWidget *probe = gtk_label_new("");
	gtk_widget_set_name(probe, name);
	gtk_widget_set_visible(probe, FALSE);
	gtk_box_append(GTK_BOX(box), probe);
	return probe;
}

static void slide_set_probes(SlideState *s, GtkWidget *accent, GtkWidget *muted, GtkWidget *success, GtkWidget *meter, GtkWidget *tile) {
	s->probe_accent = accent;
	s->probe_muted = muted;
	s->probe_success = success;
	s->probe_meter = meter;
	s->probe_tile = tile;
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

// load_css applies a stylesheet held in memory rather than one read from
// a path: themes can be built into the binary (see internal/theme), so
// 0type must not depend on a themes/ directory existing next to wherever
// it happens to be run from.
//
// One provider, reused. Adding a fresh provider per call would *stack*
// stylesheets: the newest wins wherever two themes set the same property,
// but any property the old theme set and the new one doesn't would linger
// forever. That's invisible when the theme is only loaded once at
// startup, and immediately visible when switching themes live in the
// settings menu.
static GtkCssProvider *css_provider = NULL;

static void load_css(const char *css) {
	if (css_provider == NULL) {
		css_provider = gtk_css_provider_new();
		GdkDisplay *display = gdk_display_get_default();
		gtk_style_context_add_provider_for_display(display, GTK_STYLE_PROVIDER(css_provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
	}
	gtk_css_provider_load_from_string(css_provider, css);
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

// grab_keyboard directs key events to this window. An override-redirect
// window is invisible to the window manager by design (that's how the
// overlay stays out of the taskbar and alt-tab), and the flip side is
// that nothing ever gives it keyboard focus: XSetInputFocus alone is not
// enough under Xwayland, because Mutter decides which X client holds
// focus and it has no reason to pick a window it isn't managing. An
// active grab takes the keyboard regardless, which is the same thing
// every X11 popup menu does.
//
// Returns TRUE if the grab succeeded. It can legitimately fail (another
// client already holds a grab -- a menu open elsewhere, the overview),
// in which case the caller must cope rather than assume it has input.
static gboolean grab_keyboard(GtkWidget *window) {
	GtkNative *native = gtk_widget_get_native(window);
	GdkSurface *surface = gtk_native_get_surface(native);
	if (!surface) {
		return FALSE;
	}
	Window xid = gdk_x11_surface_get_xid(surface);
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);

	// XSetInputFocus on a window that isn't viewable yet is a BadMatch,
	// and GDK's default X error handler turns that into an immediate,
	// fatal exit -- so this must never be called optimistically. The
	// window is not viewable for a short while after Show (it maps during
	// the intro animation), which is exactly when a menu wants the
	// keyboard, hence the check and the caller's retry loop.
	XWindowAttributes attrs;
	if (!XGetWindowAttributes(xdisplay, xid, &attrs) || attrs.map_state != IsViewable) {
		return FALSE;
	}

	XSetInputFocus(xdisplay, xid, RevertToPointerRoot, CurrentTime);
	int status = XGrabKeyboard(xdisplay, xid, True, GrabModeAsync, GrabModeAsync, CurrentTime);
	XFlush(xdisplay);
	return status == GrabSuccess;
}

static void ungrab_keyboard(GtkWidget *window) {
	GtkNative *native = gtk_widget_get_native(window);
	GdkSurface *surface = gtk_native_get_surface(native);
	if (!surface) {
		return;
	}
	Display *xdisplay = GDK_SURFACE_XDISPLAY(surface);
	XUngrabKeyboard(xdisplay, CurrentTime);
	XFlush(xdisplay);
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
	gboolean centered; // TRUE places the panel in the middle of the screen instead
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
	// The dictation bar sits near the bottom, out of the way of whatever
	// you're dictating into. The menu is something you look at and act on,
	// so it belongs in the middle of the screen where a launcher would be.
	int y;
	if (ctx->centered) {
		y = (screen_h - real.height) / 2;
	} else {
		y = screen_h - real.height - ctx->bottom_margin;
	}
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
static void schedule_show_animation(GtkWidget *window, GtkWidget *panel, int bottom_margin, gboolean centered, guint delay_ms, int rise_px, double duration_s) {
	ShowAnimCtx *ctx = malloc(sizeof(ShowAnimCtx));
	ctx->window = window;
	ctx->panel = panel;
	ctx->bottom_margin = bottom_margin;
	ctx->centered = centered;
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
	gboolean cancelled;
} HideAnimState;

// The one hide animation that may be in flight, so a Show arriving
// mid-outro can call it off (see cancel_hide_animation). There is exactly
// one window, so one slot is the whole story.
static HideAnimState *active_hide = NULL;

static gboolean hide_anim_tick(GtkWidget *widget, GdkFrameClock *clock, gpointer data) {
	HideAnimState *s = (HideAnimState *)data;

	// Called off by a Show that arrived while this outro was still
	// running: stop without touching the window, which is now somebody
	// else's.
	if (s->cancelled) {
		if (active_hide == s) {
			active_hide = NULL;
		}
		free(s);
		return G_SOURCE_REMOVE;
	}

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
		if (active_hide == s) {
			active_hide = NULL;
		}
		free(s);
		return G_SOURCE_REMOVE;
	}
	return G_SOURCE_CONTINUE;
}

// cancel_hide_animation calls off an outro that hasn't finished. Without
// this, showing the window again during the ~320ms it takes to close left
// the finishing tick to unmap the window that had just been re-shown: the
// panel vanished and, because the caller believed it was on screen, every
// later attempt to open it did nothing at all. Reaching that state needed
// nothing more exotic than closing the menu and immediately reopening it.
static void cancel_hide_animation(void) {
	if (active_hide != NULL) {
		active_hide->cancelled = TRUE;
		active_hide = NULL;
	}
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
	s->cancelled = FALSE;

	cancel_hide_animation(); // never run two outros at once
	active_hide = s;
	gtk_widget_add_tick_callback(window, hide_anim_tick, s, NULL);
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
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
	// menuWidthPx is wider than the bar: menu rows carry a label and a
	// right-aligned value, and at the bar's width the two collide.
	menuWidthPx = 340
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

	// copiedText is the theme's wording for the end-of-session
	// confirmation; empty means the built-in phrasing.
	copiedText string

	// centered selects where the next Show puts the window: the middle of
	// the screen (the menu, which you look at) rather than near the bottom
	// (the dictation bar, which should stay out of the way of whatever
	// you're typing into).
	centered bool
}

// New creates the window and builds its widget tree: a styled panel
// containing a fixed-size drawing area that the transcript text slides
// around inside as it comes in (see SetText). Pass "" for initialText to
// start idle (a pulsing dot; see slide_draw) rather than with text
// already showing. It must be called from the same goroutine that will
// later call Run.
func New(initialText string) (*Window, error) {
	// Before GTK builds its font map: fonts registered afterwards may not
	// be picked up by an already-initialized Pango context.
	if err := registerBundledFonts(); err != nil {
		// Cosmetic, not fatal -- a theme that wanted a bundled font falls
		// back to its next choice.
		fmt.Fprintln(os.Stderr, "0type:", err)
	}

	if C.gtk_init_check() == C.FALSE {
		return nil, fmt.Errorf("ui: gtk_init_check failed (no display? is Xwayland available?)")
	}

	win := C.new_window()

	box := C.new_box_vertical()
	withCString("zt-panel", func(c *C.char) { C.widget_set_name(box, c) })

	slide := C.new_slide_area(viewportWidthPx, viewportHeightPx)
	withCString("zt-label", func(c *C.char) { C.widget_set_name(slide.area, c) })

	C.install_key_controller(win)
	C.box_append(box, slide.area)
	C.slide_set_probes(slide,
		newColorProbe(box, "zt-accent"),
		newColorProbe(box, "zt-muted"),
		newColorProbe(box, "zt-success"),
		newColorProbe(box, "zt-meter"),
		newColorProbe(box, "zt-tile"),
	)
	C.window_set_child(win, box)

	withCString(initialText, func(c *C.char) { C.slide_retarget(slide, viewportWidthPx, c) })

	return &Window{win: win, panel: box, slide: slide}, nil
}

// newColorProbe adds one invisible CSS-color carrier to the panel; see
// new_color_probe for why the drawn content needs them.
func newColorProbe(box *C.GtkWidget, name string) *C.GtkWidget {
	var probe *C.GtkWidget
	withCString(name, func(c *C.char) { probe = C.new_color_probe(box, c) })
	return probe
}

// LoadCSS applies a stylesheet application-wide, from memory (themes are
// embedded in the binary; see internal/theme). The selectors a theme is
// expected to set are #zt-panel and #zt-label for the panel and its text,
// plus #zt-accent, #zt-muted, #zt-success, #zt-meter and #zt-tile, whose
// `color` drives the Cairo-drawn brand mark, idle placeholder,
// confirmation, level meter and mark tile respectively (see
// new_color_probe). Anything a theme leaves unset falls back to GTK's own
// default for that property, which for a missing `color` is rarely what
// the theme author wants -- the bundled themes set all of them.
func (w *Window) LoadCSS(css []byte) {
	withCString(string(css), func(c *C.char) { C.load_css(c) })
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

// Mark names one of the brand marks the bar can draw at its left edge.
// Which one is used is a theme's choice (see internal/theme).
type Mark string

const (
	// MarkMic is a smooth vector microphone: the default.
	MarkMic Mark = "mic"
	// MarkPixel is an 8-bit microphone drawn as square cells, with a hard-
	// cornered tile to match. For themes where a smooth curve would look
	// out of place.
	MarkPixel Mark = "pixel"
	// MarkZero is a geometric "0" -- a ring with a center dot.
	MarkZero Mark = "zero"
)

// Marks is every mark a theme may name.
var Marks = []Mark{MarkMic, MarkPixel, MarkZero}

// Valid reports whether m is a mark the overlay knows how to draw.
func (m Mark) Valid() bool {
	for _, known := range Marks {
		if m == known {
			return true
		}
	}
	return false
}

// SetMark chooses the brand mark drawn at the left of the bar. An unknown
// or empty mark falls back to MarkMic. Must be called from the GTK main
// thread -- use RunOnMainThread from any other goroutine.
func (w *Window) SetMark(m Mark) {
	code := C.int(C.MARK_MIC)
	switch m {
	case MarkPixel:
		code = C.MARK_PIXEL
	case MarkZero:
		code = C.MARK_ZERO
	}
	C.slide_set_mark(w.slide, code)
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
	text := w.copiedText
	if text == "" {
		text = "Copied to clipboard"
	}
	withCString(text, func(c *C.char) { C.slide_show_confirmation(w.slide, c) })
}

// Show makes the window visible and plays its intro animation (fade in
// while easing up into its final resting position; see
// schedule_show_animation). Must be called from the GTK main thread --
// use RunOnMainThread from any other goroutine.
func (w *Window) Show() {
	// Call off any outro still in flight. It would otherwise finish by
	// unmapping the window this call is about to show, leaving the app
	// convinced its panel is on screen when nothing is -- an
	// unrecoverable state, since the next open sees "already open" and
	// does nothing.
	C.cancel_hide_animation()

	// Opacity must already be 0 *before* the window becomes visible, not
	// only later once schedule_show_animation's delayed callback gets
	// around to it -- otherwise the window flashes in at full opacity
	// (wherever it was last positioned) for the whole repositionDelayMs
	// wait, then jumps to the animation's start state. That was the bug
	// behind the intro "not being seen properly": there was a real,
	// visible flash-then-jump before any fade/rise ever started.
	C.widget_set_opacity(w.panel, 0.0)
	C.widget_set_visible(w.win, C.TRUE)
	centered := C.gboolean(C.FALSE)
	if w.centered {
		centered = C.TRUE
	}
	C.schedule_show_animation(w.win, w.panel, C.int(bottomMarginPx), centered, repositionDelayMs, showAnimRisePx, showAnimDurationS)
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

// MenuItem is one row of the overlay's menu: a label, and optional
// secondary text shown right-aligned (the current value, or a hint).
type MenuItem struct {
	Label  string
	Detail string
}

// ShowMenu switches the panel from the dictation bar to a menu of items,
// with the given row selected, and resizes the window to fit. Call
// DismissMenu to go back. Must be called from the GTK main thread -- use
// RunOnMainThread from any other goroutine.
func (w *Window) ShowMenu(items []MenuItem, selected int) {
	C.menu_clear(w.slide)
	for _, item := range items {
		withCString(item.Label, func(label *C.char) {
			withCString(item.Detail, func(detail *C.char) {
				C.menu_add(w.slide, label, detail)
			})
		})
	}
	C.menu_commit(w.slide, w.win, w.panel, menuWidthPx, C.int(selected))
}

// SelectMenuItem moves the menu's selection. The highlight animates to
// the new row. Must be called from the GTK main thread.
func (w *Window) SelectMenuItem(index int) {
	C.menu_set_selected(w.slide, C.int(index))
}

// DismissMenu returns the panel to the dictation bar. Must be called from
// the GTK main thread.
func (w *Window) DismissMenu() {
	C.menu_dismiss(w.slide, w.win, w.panel, viewportWidthPx, viewportHeightPx)
}

// MaxArtSize is the largest sprite a theme may supply to SetMarkArt.
const MaxArtSize = 16

// SetMarkArt gives the overlay a theme-supplied sprite to draw in place
// of the built-in mark: one string per row, '#' for a filled cell and
// anything else for empty. Rows must all be the same length and neither
// dimension may exceed MaxArtSize. Passing nil clears it. Must be called
// from the GTK main thread.
func (w *Window) SetMarkArt(rows []string) {
	if len(rows) == 0 || len(rows) > MaxArtSize {
		C.slide_set_art(w.slide, nil, 0, 0)
		return
	}
	cols := len(rows[0])
	if cols == 0 || cols > MaxArtSize {
		C.slide_set_art(w.slide, nil, 0, 0)
		return
	}
	var flat strings.Builder
	for _, row := range rows {
		if len(row) != cols {
			C.slide_set_art(w.slide, nil, 0, 0) // ragged: refuse rather than draw garbage
			return
		}
		flat.WriteString(row)
	}
	withCString(flat.String(), func(c *C.char) {
		C.slide_set_art(w.slide, c, C.int(len(rows)), C.int(cols))
	})
}

// SetIdleText sets the placeholder shown when there's no transcript yet,
// and SetCopiedText the end-of-session confirmation. Both are a theme's
// choice (see internal/theme). Empty restores the built-in wording. Must
// be called from the GTK main thread.
func (w *Window) SetIdleText(text string) {
	withCString(text, func(c *C.char) { C.slide_set_idle_text(w.slide, c) })
}

// SetCopiedText sets the wording of the "copied" confirmation.
func (w *Window) SetCopiedText(text string) {
	w.copiedText = text
}

// SetCentered chooses where the next Show places the window: centered on
// screen when true, near the bottom edge when false (the default). Must
// be called before Show.
func (w *Window) SetCentered(centered bool) {
	w.centered = centered
}

// Key is a keyboard key the overlay reacts to. Only the handful the menu
// needs are named; everything else arrives as KeyOther.
type Key int

const (
	KeyOther Key = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEscape
)

// keyHandler is the single, process-wide key callback. GTK delivers key
// events on the main thread, and 0type has exactly one window, so a
// package-level variable is honest here -- a registry keyed by window
// would be indirection with nothing to point at. Guarded anyway because
// SetKeyHandler may be called before Run from a different goroutine.
var (
	keyMu      sync.Mutex
	keyHandler func(Key) bool
)

// SetKeyHandler installs fn as the overlay's keyboard handler; it is
// called on the GTK main thread for every key press while the window has
// the keyboard, and should return true if it consumed the key. Pass nil
// to stop handling keys.
func (w *Window) SetKeyHandler(fn func(Key) bool) {
	keyMu.Lock()
	keyHandler = fn
	keyMu.Unlock()
}

// GrabKeyboard directs key events to the overlay. It retries for a short
// while because the window is not yet viewable for the first frames after
// Show -- and an override-redirect window can't be focused until it is.
// Calls back with whether the keyboard was obtained; a false means
// something else holds a grab (another popup, the GNOME overview) and the
// caller must not pretend it has input. Must be called from the GTK main
// thread; done reports on that thread too.
func (w *Window) GrabKeyboard(done func(bool)) {
	const (
		attempts = 20
		interval = 25 * time.Millisecond
	)
	var try func(left int)
	try = func(left int) {
		if C.grab_keyboard(w.win) == C.TRUE {
			done(true)
			return
		}
		if left <= 0 {
			done(false)
			return
		}
		time.AfterFunc(interval, func() { RunOnMainThread(func() { try(left - 1) }) })
	}
	try(attempts)
}

// ReleaseKeyboard gives the keyboard back. Every GrabKeyboard must be
// paired with one of these, including on the paths where the window is
// closing -- a keyboard grab that outlives its window locks the user out
// of their session.
func (w *Window) ReleaseKeyboard() {
	C.ungrab_keyboard(w.win)
}

//export goKeyPressed
func goKeyPressed(keyval C.guint) C.gboolean {
	keyMu.Lock()
	fn := keyHandler
	keyMu.Unlock()
	if fn == nil {
		return C.FALSE
	}

	var key Key
	switch keyval {
	case C.GDK_KEY_Up:
		key = KeyUp
	case C.GDK_KEY_Down:
		key = KeyDown
	case C.GDK_KEY_Return, C.GDK_KEY_KP_Enter:
		key = KeyEnter
	case C.GDK_KEY_Escape:
		key = KeyEscape
	default:
		key = KeyOther
	}

	if fn(key) {
		return C.TRUE
	}
	return C.FALSE
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
