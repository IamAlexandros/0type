package main

import (
	"log"
	"time"

	"github.com/IamAlexandros/0type/internal/config"
	"github.com/IamAlexandros/0type/internal/theme"
	"github.com/IamAlexandros/0type/internal/ui"
)

// menuPage is which list the menu is currently showing.
type menuPage int

const (
	pageRoot menuPage = iota
	pageThemes
)

// menuState is the menu's whole model: which page, which row, and what to
// put back if the user backs out of a live preview. It is only ever
// touched on the GTK main thread (key events and signal handlers both
// arrive there), so it needs no lock of its own -- unlike the dictation
// state in app, which is shared with the capture goroutine.
type menuState struct {
	open     bool
	page     menuPage
	selected int

	// token identifies the current menu session, so a timeout scheduled
	// for one open menu can't close a later one.
	token  int
	themes []theme.Entry
	// themeBefore is the theme that was active when the theme page was
	// opened, restored if the user backs out. Browsing themes applies them
	// live, so leaving without choosing has to undo that -- otherwise
	// "just looking" silently changes your setup.
	themeBefore string
}

// menuIdleTimeout closes an untouched menu, releasing the keyboard.
//
// This is not a nicety. Driving the menu requires an XGrabKeyboard (an
// override-redirect window gets no focus otherwise -- see
// internal/ui.GrabKeyboard), and while that grab is held the compositor
// never sees key presses, which means 0type's *own* global shortcut stops
// working. A menu left open on screen therefore breaks the main way the
// program is used, so an idle one has to let go by itself.
const menuIdleTimeout = 20 * time.Second

// openMenu shows the root menu, taking the keyboard so arrow keys work.
// Anything already on screen (a dictation session) is ended first: the
// menu and the bar are the same panel and can't both be showing.
func (a *app) openMenu() {
	// Deliberately not "if already open, do nothing": asking for the menu
	// re-renders and re-shows it unconditionally, so if the app's idea of
	// what's on screen ever drifts from what actually is, running `0type`
	// again fixes it instead of silently doing nothing forever.
	if a.isVisible() {
		a.hideAndStop()
	}
	a.menu.open = true
	a.menu.token++
	a.menu.page = pageRoot
	a.menu.selected = 0
	a.renderMenu()
	a.resetMenuTimeout()
	a.win.SetCentered(true)
	a.win.Show()
	a.win.SetKeyHandler(a.handleMenuKey)
	a.win.GrabKeyboard(func(ok bool) {
		if !ok {
			// Without the keyboard the menu can't be driven, and an
			// undismissable panel is worse than no menu, so back out.
			log.Print("0type: could not take the keyboard (another window may have it); closing the menu")
			a.closeMenu()
		}
	})
}

// resetMenuTimeout restarts the idle countdown; called on every key so
// the menu only closes itself when genuinely untouched. Bumping the token
// is what cancels the previous countdown -- without it the timer armed
// when the menu opened would still fire on schedule and close a menu
// somebody was actively using.
func (a *app) resetMenuTimeout() {
	a.menu.token++
	token := a.menu.token
	time.AfterFunc(menuIdleTimeout, func() {
		ui.RunOnMainThread(func() {
			if a.menu.open && a.menu.token == token {
				a.closeMenu()
			}
		})
	})
}

// closeMenu hides the menu and gives the keyboard back.
func (a *app) closeMenu() {
	if !a.menu.open {
		return
	}
	a.menu.open = false
	a.menu.token++ // invalidate any pending idle timeout
	a.win.SetKeyHandler(nil)
	a.win.ReleaseKeyboard()
	a.win.Hide()
	// Restore the bar layout and placement once the window is out of
	// sight, so neither the resize nor the move is visible mid-animation.
	a.win.DismissMenu()
	a.win.SetCentered(false)
}

// Row indices in the root menu. Named because "case 2" stops meaning
// anything the moment a row is inserted -- and one of these rows shuts
// the program down.
const (
	rowDictate = iota
	rowTheme
	rowClose
	rowQuit
)

// rootItems is the menu's top level. Deliberately short: this is a
// dictation tool, and every row here is something a person might
// plausibly want that isn't already a keyboard shortcut.
//
// Closing the menu and quitting 0type are separate rows because they are
// wildly different actions that were previously one keystroke apart.
// Quitting stops the resident process, so the next shortcut press has to
// load the model again -- not what anyone means by "close this".
func (a *app) rootItems() []ui.MenuItem {
	return []ui.MenuItem{
		{Label: "Start dictation", Detail: "⏎"},
		{Label: "Theme", Detail: a.themeName},
		{Label: "Close menu", Detail: "esc"},
		{Label: "Quit 0type", Detail: "stops it running"},
	}
}

func (a *app) themeItems() []ui.MenuItem {
	items := make([]ui.MenuItem, 0, len(a.menu.themes)+1)
	for _, e := range a.menu.themes {
		detail := ""
		if e.Name == a.themeName {
			detail = "current"
		}
		items = append(items, ui.MenuItem{Label: e.Name, Detail: detail})
	}
	return items
}

func (a *app) renderMenu() {
	var items []ui.MenuItem
	if a.menu.page == pageThemes {
		items = a.themeItems()
	} else {
		items = a.rootItems()
	}
	a.win.ShowMenu(items, a.menu.selected)
}

// handleMenuKey drives the menu. Runs on the GTK main thread.
func (a *app) handleMenuKey(k ui.Key) bool {
	if !a.menu.open {
		return false
	}

	count := len(a.rootItems())
	if a.menu.page == pageThemes {
		count = len(a.menu.themes)
	}
	if count == 0 {
		return true
	}
	a.resetMenuTimeout()

	switch k {
	case ui.KeyUp, ui.KeyDown:
		delta := 1
		if k == ui.KeyUp {
			delta = -1
		}
		// Wrap around: with three rows, making someone stop at the end is
		// just extra keystrokes.
		a.menu.selected = (a.menu.selected + delta + count) % count
		a.win.SelectMenuItem(a.menu.selected)
		if a.menu.page == pageThemes {
			a.previewTheme(a.menu.themes[a.menu.selected].Name)
		}
		return true

	case ui.KeyEnter:
		a.activateMenuItem()
		return true

	case ui.KeyEscape:
		if a.menu.page == pageThemes {
			a.previewTheme(a.menu.themeBefore) // backing out undoes the preview
			a.menu.page = pageRoot
			a.menu.selected = rowTheme // back on the row they came from
			a.renderMenu()
			return true
		}
		a.closeMenu()
		return true
	}

	// Any other key closes the menu. While it's open the keyboard is
	// grabbed, so a key meant for another window is swallowed anyway --
	// and, worse, so is 0type's own global shortcut, because the
	// compositor never sees it. Dismissing on the first unrecognized
	// press limits that to a single lost keystroke instead of leaving the
	// keyboard captured until someone finds the menu and presses Escape.
	a.closeMenu()
	return true
}

func (a *app) activateMenuItem() {
	if a.menu.page == pageThemes {
		chosen := a.menu.themes[a.menu.selected].Name
		if err := config.SetTheme(chosen); err != nil {
			log.Printf("0type: could not save the theme: %v", err)
		}
		a.themeName = chosen
		a.menu.page = pageRoot
		a.menu.selected = rowTheme
		a.renderMenu()
		return
	}

	switch a.menu.selected {
	case rowDictate:
		a.closeMenu()
		a.showAndListen()
	case rowTheme:
		a.menu.themes = theme.List()
		a.menu.themeBefore = a.themeName
		a.menu.page = pageThemes
		a.menu.selected = 0
		for i, e := range a.menu.themes {
			if e.Name == a.themeName {
				a.menu.selected = i
			}
		}
		a.renderMenu()
		a.previewTheme(a.menu.themes[a.menu.selected].Name)
	case rowClose:
		a.closeMenu()
	case rowQuit:
		a.closeMenu()
		a.win.Quit()
	}
}

// previewTheme applies a theme to the live window without saving it, so
// moving through the list shows each one immediately -- the whole point
// of choosing a theme from inside the thing being themed.
func (a *app) previewTheme(name string) {
	th, err := theme.Load(name)
	if err != nil {
		log.Printf("0type: could not load theme %q: %v", name, err)
		return
	}
	applyTheme(a.win, th)
}
