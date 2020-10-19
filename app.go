package senpai

import (
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"os/exec"
	"strings"
	"time"

	"git.sr.ht/~taiite/senpai/irc"
	"git.sr.ht/~taiite/senpai/ui"
	"github.com/gdamore/tcell/v2"
)

type App struct {
	win     *ui.UI
	s       *irc.Session
	pasting bool

	cfg        Config
	highlights []string

	lastQuery string
}

func NewApp(cfg Config) (app *App, err error) {
	app = &App{
		cfg: cfg,
	}

	if cfg.Highlights != nil {
		app.highlights = make([]string, len(cfg.Highlights))
		for i := range app.highlights {
			app.highlights[i] = strings.ToLower(cfg.Highlights[i])
		}
	}

	app.win, err = ui.New(ui.Config{
		NickColWidth: cfg.NickColWidth,
		AutoComplete: func(cursorIdx int, text []rune) []ui.Completion {
			return app.completions(cursorIdx, text)
		},
	})
	if err != nil {
		return
	}

	app.initWindow()

	var conn *tls.Conn
	app.addLineNow(Home, ui.Line{
		Head: "--",
		Body: fmt.Sprintf("Connecting to %s...", cfg.Addr),
	})
	conn, err = tls.Dial("tcp", cfg.Addr, nil)
	if err != nil {
		app.addLineNow(Home, ui.Line{
			Head:      "!!",
			HeadColor: ui.ColorRed,
			Body:      "Connection failed",
		})
		err = nil
		return
	}

	var auth irc.SASLClient
	if cfg.Password != nil {
		auth = &irc.SASLPlain{Username: cfg.User, Password: *cfg.Password}
	}
	app.s, err = irc.NewSession(conn, irc.SessionParams{
		Nickname: cfg.Nick,
		Username: cfg.User,
		RealName: cfg.Real,
		Auth:     auth,
		Debug:    cfg.Debug,
	})
	if err != nil {
		app.addLineNow(Home, ui.Line{
			Head:      "!!",
			HeadColor: ui.ColorRed,
			Body:      "Registration failed",
		})
	}

	return
}

func (app *App) Close() {
	app.win.Close()
	if app.s != nil {
		app.s.Stop()
	}
}

func (app *App) Run() {
	for !app.win.ShouldExit() {
		if app.s != nil {
			select {
			case ev := <-app.s.Poll():
				evs := []irc.Event{ev}
			Batch:
				for i := 0; i < 64; i++ {
					select {
					case ev := <-app.s.Poll():
						evs = append(evs, ev)
					default:
						break Batch
					}
				}
				app.handleIRCEvents(evs)
			case ev := <-app.win.Events:
				app.handleUIEvent(ev)
			}
		} else {
			ev := <-app.win.Events
			app.handleUIEvent(ev)
		}
	}
}

func (app *App) handleIRCEvents(evs []irc.Event) {
	for _, ev := range evs {
		app.handleIRCEvent(ev)
	}
	if !app.pasting {
		app.draw()
	}
}

func (app *App) handleIRCEvent(ev irc.Event) {
	switch ev := ev.(type) {
	case irc.RawMessageEvent:
		head := "IN --"
		if ev.Outgoing {
			head = "OUT --"
		} else if !ev.IsValid {
			head = "IN ??"
		}
		app.win.AddLine(Home, false, ui.Line{
			At:   time.Now(),
			Head: head,
			Body: ev.Message,
		})
	case irc.RegisteredEvent:
		body := "Connected to the server"
		if app.s.Nick() != app.cfg.Nick {
			body += " as " + app.s.Nick()
		}
		app.win.AddLine(Home, false, ui.Line{
			At:   time.Now(),
			Head: "--",
			Body: body,
		})
	case irc.SelfNickEvent:
		app.win.AddLine(app.win.CurrentBuffer(), true, ui.Line{
			At:        ev.Time,
			Head:      "--",
			Body:      fmt.Sprintf("\x0314%s\x03\u2192\x0314%s\x03", ev.FormerNick, app.s.Nick()),
			Highlight: true,
		})
	case irc.UserNickEvent:
		for _, c := range app.s.ChannelsSharedWith(ev.User.Name) {
			app.win.AddLine(c, false, ui.Line{
				At:        ev.Time,
				Head:      "--",
				Body:      fmt.Sprintf("\x0314%s\x03\u2192\x0314%s\x03", ev.FormerNick, ev.User.Name),
				Mergeable: true,
			})
		}
	case irc.SelfJoinEvent:
		app.win.AddBuffer(ev.Channel)
		app.s.RequestHistory(ev.Channel, time.Now())
	case irc.UserJoinEvent:
		app.win.AddLine(ev.Channel, false, ui.Line{
			At:        time.Now(),
			Head:      "--",
			Body:      fmt.Sprintf("\x033+\x0314%s\x03", ev.User.Name),
			Mergeable: true,
		})
	case irc.SelfPartEvent:
		app.win.RemoveBuffer(ev.Channel)
	case irc.UserPartEvent:
		app.win.AddLine(ev.Channel, false, ui.Line{
			At:        ev.Time,
			Head:      "--",
			Body:      fmt.Sprintf("\x034-\x0314%s\x03", ev.User.Name),
			Mergeable: true,
		})
	case irc.UserQuitEvent:
		for _, c := range ev.Channels {
			app.win.AddLine(c, false, ui.Line{
				At:        ev.Time,
				Head:      "--",
				Body:      fmt.Sprintf("\x034-\x0314%s\x03", ev.User.Name),
				Mergeable: true,
			})
		}
	case irc.TopicChangeEvent:
		app.win.AddLine(ev.Channel, false, ui.Line{
			At:   ev.Time,
			Head: "--",
			Body: fmt.Sprintf("\x0314Topic changed to: %s\x03", ev.Topic),
		})
	case irc.MessageEvent:
		buffer, line, hlNotification := app.formatMessage(ev)
		app.win.AddLine(buffer, hlNotification, line)
		if hlNotification {
			app.notifyHighlight(buffer, ev.User.Name, ev.Content)
		}
		if !ev.TargetIsChannel && app.s.NickCf() != app.s.Casemap(ev.User.Name) {
			app.lastQuery = ev.User.Name
		}
	case irc.HistoryEvent:
		var lines []ui.Line
		for _, m := range ev.Messages {
			switch m := m.(type) {
			case irc.MessageEvent:
				_, line, _ := app.formatMessage(m)
				lines = append(lines, line)
			default:
			}
		}
		app.win.AddLines(ev.Target, lines)
	case error:
		panic(ev)
	}
}

func (app *App) handleUIEvent(ev tcell.Event) {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		app.win.Resize()
	case *tcell.EventPaste:
		app.pasting = ev.Start()
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyCtrlC:
			app.win.Exit()
		case tcell.KeyCtrlL:
			app.win.Resize()
		case tcell.KeyCtrlU, tcell.KeyPgUp:
			app.win.ScrollUp()
			if app.s == nil {
				return
			}
			buffer := app.win.CurrentBuffer()
			if app.win.IsAtTop() && buffer != Home {
				at := time.Now()
				if t := app.win.CurrentBufferOldestTime(); t != nil {
					at = *t
				}
				app.s.RequestHistory(buffer, at)
			}
		case tcell.KeyCtrlD, tcell.KeyPgDn:
			app.win.ScrollDown()
		case tcell.KeyCtrlN:
			app.win.NextBuffer()
		case tcell.KeyCtrlP:
			app.win.PreviousBuffer()
		case tcell.KeyRight:
			if ev.Modifiers() == tcell.ModAlt {
				app.win.NextBuffer()
			} else {
				app.win.InputRight()
			}
		case tcell.KeyLeft:
			if ev.Modifiers() == tcell.ModAlt {
				app.win.PreviousBuffer()
			} else {
				app.win.InputLeft()
			}
		case tcell.KeyUp:
			app.win.InputUp()
		case tcell.KeyDown:
			app.win.InputDown()
		case tcell.KeyHome:
			app.win.InputHome()
		case tcell.KeyEnd:
			app.win.InputEnd()
		case tcell.KeyBackspace2:
			ok := app.win.InputBackspace()
			if ok {
				app.typing()
			}
		case tcell.KeyDelete:
			ok := app.win.InputDelete()
			if ok {
				app.typing()
			}
		case tcell.KeyTab:
			ok := app.win.InputAutoComplete()
			if ok {
				app.typing()
			}
		case tcell.KeyCR, tcell.KeyLF:
			buffer := app.win.CurrentBuffer()
			input := app.win.InputEnter()
			err := app.handleInput(buffer, input)
			if err != nil {
				app.win.AddLine(app.win.CurrentBuffer(), false, ui.Line{
					At:        time.Now(),
					Head:      "!!",
					HeadColor: ui.ColorRed,
					Body:      fmt.Sprintf("%q: %s", input, err),
				})
			}
		case tcell.KeyRune:
			app.win.InputRune(ev.Rune())
			app.typing()
		default:
			return
		}
	default:
		return
	}
	if !app.pasting {
		app.draw()
	}
}

func (app *App) isHighlight(content string) bool {
	contentCf := strings.ToLower(content)
	if app.highlights == nil {
		return strings.Contains(contentCf, app.s.NickCf())
	}
	for _, h := range app.highlights {
		if strings.Contains(contentCf, h) {
			return true
		}
	}
	return false
}

func (app *App) notifyHighlight(buffer, nick, content string) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		return
	}
	here := "0"
	if buffer == app.win.CurrentBuffer() {
		here = "1"
	}
	r := strings.NewReplacer(
		"%%", "%",
		"%b", buffer,
		"%h", here,
		"%n", nick,
		"%m", cleanMessage(content))
	command := r.Replace(app.cfg.OnHighlight)
	err = exec.Command(sh, "-c", command).Run()
	if err != nil {
		app.win.AddLine(Home, false, ui.Line{
			At:        time.Now(),
			Head:      "ERROR --",
			HeadColor: ui.ColorRed,
			Body:      fmt.Sprintf("Failed to invoke on-highlight command: %v", err),
		})
	}
}

func (app *App) typing() {
	if app.s == nil {
		return
	}
	buffer := app.win.CurrentBuffer()
	if buffer == Home {
		return
	}
	if app.win.InputLen() == 0 {
		app.s.TypingStop(buffer)
	} else if !app.win.InputIsCommand() {
		app.s.Typing(app.win.CurrentBuffer())
	}
}

func (app *App) completions(cursorIdx int, text []rune) []ui.Completion {
	var cs []ui.Completion

	if len(text) == 0 {
		return cs
	}

	var start int
	for start = cursorIdx - 1; 0 <= start; start-- {
		if text[start] == ' ' {
			break
		}
	}
	start++
	word := text[start:cursorIdx]
	wordCf := app.s.Casemap(string(word))
	for _, name := range app.s.Names(app.win.CurrentBuffer()) {
		if strings.HasPrefix(app.s.Casemap(name.Name.Name), wordCf) {
			nickComp := []rune(name.Name.Name)
			if start == 0 {
				nickComp = append(nickComp, ':')
			}
			nickComp = append(nickComp, ' ')
			c := make([]rune, len(text)+len(nickComp)-len(word))
			copy(c[:start], text[:start])
			if cursorIdx < len(text) {
				copy(c[start+len(nickComp):], text[cursorIdx:])
			}
			copy(c[start:], nickComp)
			cs = append(cs, ui.Completion{
				Text:      c,
				CursorIdx: start + len(nickComp),
			})
		}
	}

	if cs != nil {
		cs = append(cs, ui.Completion{
			Text:      text,
			CursorIdx: cursorIdx,
		})
	}

	return cs
}

func (app *App) formatMessage(ev irc.MessageEvent) (buffer string, line ui.Line, hlNotification bool) {
	isFromSelf := app.s.NickCf() == app.s.Casemap(ev.User.Name)
	isHighlight := app.isHighlight(ev.Content)
	isAction := strings.HasPrefix(ev.Content, "\x01ACTION")
	isQuery := !ev.TargetIsChannel && ev.Command == "PRIVMSG"
	isNotice := ev.Command == "NOTICE"

	if !ev.TargetIsChannel && isNotice {
		buffer = app.win.CurrentBuffer()
	} else if !ev.TargetIsChannel {
		buffer = Home
	} else {
		buffer = ev.Target
	}

	hlLine := ev.TargetIsChannel && isHighlight && !isFromSelf
	hlNotification = (isHighlight || isQuery) && !isFromSelf

	head := ev.User.Name
	headColor := ui.ColorWhite
	if isFromSelf && isQuery {
		head = "\u2192 " + ev.Target
		headColor = app.identColor(ev.Target)
	} else if isAction || isNotice {
		head = "*"
	} else {
		headColor = app.identColor(head)
	}

	body := strings.TrimSuffix(ev.Content, "\x01")
	if isNotice && isAction {
		c := ircColorSequence(app.identColor(ev.User.Name))
		body = fmt.Sprintf("(%s%s\x0F:%s)", c, ev.User.Name, body[7:])
	} else if isAction {
		c := ircColorSequence(app.identColor(ev.User.Name))
		body = fmt.Sprintf("%s%s\x0F%s", c, ev.User.Name, body[7:])
	} else if isNotice {
		c := ircColorSequence(app.identColor(ev.User.Name))
		body = fmt.Sprintf("(%s%s\x0F: %s)", c, ev.User.Name, body)
	}

	line = ui.Line{
		At:        ev.Time,
		Head:      head,
		Body:      body,
		HeadColor: headColor,
		Highlight: hlLine,
	}
	return
}

func ircColorSequence(code int) string {
	var c [3]rune
	c[0] = 0x03
	c[1] = rune(code/10) + '0'
	c[2] = rune(code%10) + '0'
	return string(c[:])
}

// see <https://modern.ircdocs.horse/formatting.html>
var identColorBlacklist = []int{1, 8, 16, 27, 28, 88, 89, 90, 91}

func (app *App) identColor(s string) (code int) {
	h := fnv.New32()
	_, _ = h.Write([]byte(s))

	code = int(h.Sum32()) % (99 - len(identColorBlacklist))
	for _, c := range identColorBlacklist {
		if c <= code {
			code++
		}
	}

	return
}

func cleanMessage(s string) string {
	var res strings.Builder
	var sb ui.StyleBuffer
	res.Grow(len(s))
	for _, r := range s {
		if _, ok := sb.WriteRune(r); ok != 0 {
			if 1 < ok {
				res.WriteRune(',')
			}
			res.WriteRune(r)
		}
	}
	return res.String()
}
