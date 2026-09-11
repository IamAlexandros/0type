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
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <X11/Xlib.h>
#include <glib-unix.h>
#include <stdlib.h>
#include <stdio.h>

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

static GtkWidget *new_label(const char *text) {
	return gtk_label_new(text);
}

static void label_set_text(GtkWidget *label, const char *text) {
	gtk_label_set_text(GTK_LABEL(label), text);
}

// label_set_growing_line configures the label as a fixed-width single
// line that ellipsizes at the *start* ("…rest of it") rather than
// wrapping or resizing. As the transcript grows, already-shown words stay
// put and new ones appear at the right; once the line is longer than the
// box, the oldest (leftmost) words are the ones hidden behind the
// ellipsis -- so the sentence visibly "builds" toward its final form
// without the box ever changing size or the display jumping around.
static void label_set_growing_line(GtkWidget *label, int width_chars) {
	GtkLabel *l = GTK_LABEL(label);
	gtk_label_set_wrap(l, FALSE);
	gtk_label_set_single_line_mode(l, TRUE);
	gtk_label_set_ellipsize(l, PANGO_ELLIPSIZE_START);
	gtk_label_set_width_chars(l, width_chars);
	gtk_label_set_xalign(l, 0.0);
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
	// labelWidthChars fixes the label's visible width (in characters): the
	// box never grows or wraps as the transcript comes in. See
	// label_set_growing_line -- text ellipsizes at the *start* once it
	// outgrows this, so the sentence visibly builds up with its newest
	// words always in view. Tuned for the default theme's font size and
	// panel padding/margin.
	labelWidthChars = 28
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
	label *C.GtkWidget
	loop  *C.GMainLoop
}

// New creates the window and builds its widget tree (a styled panel
// containing a text label). It must be called from the same goroutine
// that will later call Run.
func New(initialText string) (*Window, error) {
	if C.gtk_init_check() == C.FALSE {
		return nil, fmt.Errorf("ui: gtk_init_check failed (no display? is Xwayland available?)")
	}

	win := C.new_window()

	box := C.new_box_vertical()
	withCString("zt-panel", func(c *C.char) { C.widget_set_name(box, c) })

	label := withCStringRet(initialText, func(c *C.char) *C.GtkWidget { return C.new_label(c) })
	withCString("zt-label", func(c *C.char) { C.widget_set_name(label, c) })
	C.label_set_growing_line(label, labelWidthChars)

	C.box_append(box, label)
	C.window_set_child(win, box)

	return &Window{win: win, label: label}, nil
}

// LoadCSS applies the stylesheet at path application-wide. Selectors
// #zt-panel and #zt-label target this window's widgets (see New).
func (w *Window) LoadCSS(path string) {
	withCString(path, func(c *C.char) { C.load_css(c) })
}

// SetText updates the label's text. The label is a fixed-width single
// line that ellipsizes at the start (see label_set_growing_line), so
// calling this with progressively longer transcript text makes the
// sentence appear to build up in place, newest words always visible,
// rather than jumping around or resizing. Must be called from the GTK
// main thread -- use RunOnMainThread from any other goroutine.
func (w *Window) SetText(text string) {
	withCString(text, func(c *C.char) { C.label_set_text(w.label, c) })
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
