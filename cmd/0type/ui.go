package main

import (
	"flag"
	"fmt"

	"github.com/IamAlexandros/0type/internal/config"
	"github.com/IamAlexandros/0type/internal/theme"
	"github.com/IamAlexandros/0type/internal/ui"
)

// runUI shows the floating overlay window in its idle state, with no live
// transcription wired in -- that's runApp. It was Phase 4's manual proof
// point (that the window anchors, styles, and animates correctly before
// anything depended on it), and it's now also how you preview a theme:
// `0type ui --theme light` renders the bar without loading the model or
// touching the microphone.
func runUI(args []string) error {
	fs := flag.NewFlagSet("0type ui", flag.ContinueOnError)
	themeFlag := fs.String("theme", "", "theme name or path to a .css file (overrides the config file)")
	// Themes have to be judged with text in them, not just idle: the
	// transcript's font, color and left fade are most of what a theme
	// controls, and none of them are visible in the "Listening…" state.
	textFlag := fs.String("text", "", "show this transcript text instead of the idle state")
	levelFlag := fs.Float64("level", 0, "show the level meter at this level (0..1)")
	confirmFlag := fs.Bool("confirm", false, "show the end-of-session \"copied\" state")
	// The menu's hardest layout case is the theme list, which is long
	// enough to scroll; rendering it without the model or a keyboard grab
	// is the only practical way to check that layout.
	menuFlag := fs.Bool("menu", false, "show the theme menu instead of the dictation bar")
	menuSelFlag := fs.Int("menu-selected", 0, "which menu row to select (with --menu)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	th, err := loadTheme(*themeFlag, cfg)
	if err != nil {
		return err
	}

	win, err := ui.New(*textFlag)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	applyTheme(win, th)
	win.SetLevel(*levelFlag)
	if *confirmFlag {
		win.ShowCopiedConfirmation()
	}
	if *menuFlag {
		items := make([]ui.MenuItem, 0, len(theme.List()))
		for _, e := range theme.List() {
			detail := ""
			if e.Name == th.Name {
				detail = "current"
			}
			items = append(items, ui.MenuItem{Label: e.Name, Detail: detail})
		}
		win.SetCentered(true)
		win.ShowMenu(items, *menuSelFlag)
	}
	win.Show()
	win.Run()
	return nil
}
