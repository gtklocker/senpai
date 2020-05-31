package main

import (
	"crypto/tls"
	"fmt"
	"git.sr.ht/~taiite/senpai"
	"git.sr.ht/~taiite/senpai/irc"
	"git.sr.ht/~taiite/senpai/ui"
	"github.com/gdamore/tcell"
	"log"
	"os/user"
	"strings"
	"time"
)

func main() {
	tcell.SetEncodingFallback(tcell.EncodingFallbackASCII)

	usr, err := user.Current()
	if err != nil {
		log.Panicln(err)
	}

	cfg, err := senpai.LoadConfigFile(usr.HomeDir + "/.config/senpai/senpai.yaml")
	if err != nil {
		log.Panicln(err)
	}

	app, err := ui.New()
	if err != nil {
		log.Panicln(err)
	}
	defer app.Close()

	addr := cfg.Addr
	app.AddLine("home", fmt.Sprintf("Connecting to %s...", addr), time.Now())

	conn, err := tls.Dial("tcp", addr, nil)
	if err != nil {
		log.Panicln(err)
	}

	s, err := irc.NewSession(conn, irc.SessionParams{
		Nickname: "taiite",
		Username: "taiitent",
		RealName: "Taiite Ier",
		Auth:     &irc.SASLPlain{Username: cfg.User, Password: cfg.Password},
	})
	if err != nil {
		log.Panicln(err)
	}
	defer s.Stop()

	for !app.ShouldExit() {
		select {
		case ev := <-s.Poll():
			switch ev := ev.(type) {
			case irc.RegisteredEvent:
				app.AddLine("home", "Connected to the server", time.Now())
			case irc.SelfJoinEvent:
				app.AddBuffer(ev.Channel)
			case irc.ChannelMessageEvent:
				line := formatIRCMessage(ev.Nick, ev.Content)
				app.AddLine(ev.Channel, line, ev.Time)
			case error:
				log.Panicln(ev)
			}
		case ev := <-app.Events:
			switch ev := ev.(type) {
			case *tcell.EventResize:
				app.Draw()
			case *tcell.EventKey:
				switch ev.Key() {
				case tcell.KeyCtrlC:
					app.Exit()
				case tcell.KeyCtrlL:
					app.Draw()
				case tcell.KeyCtrlN:
					app.NextBuffer()
				case tcell.KeyCtrlP:
					app.PreviousBuffer()
				case tcell.KeyRight:
					if ev.Modifiers() == tcell.ModAlt {
						app.NextBuffer()
					} else {
						app.InputRight()
					}
				case tcell.KeyLeft:
					if ev.Modifiers() == tcell.ModAlt {
						app.PreviousBuffer()
					} else {
						app.InputLeft()
					}
				case tcell.KeyBackspace2:
					app.InputBackspace()
				case tcell.KeyEnter:
					content := app.InputEnter()
					handleInput(app, &s, content)
				case tcell.KeyRune:
					app.InputRune(ev.Rune())
					s.Typing(app.CurrentBuffer())
				}
			}
		}
	}
}

func parseCommand(s string) (command, args string) {
	if s == "" {
		return
	}

	if s[0] != '/' {
		args = s
		return
	}

	i := strings.IndexByte(s, ' ')
	if i < 0 {
		i = len(s) - 1
	}

	command = strings.ToUpper(s[1:i])
	args = s[i+1:]

	return
}

func handleInput(app *ui.UI, s *irc.Session, content string) {
	cmd, args := parseCommand(content)

	switch cmd {
	case "":
		ch := app.CurrentBuffer()
		if ch == "home" {
			return
		}

		s.PrivMsg(ch, args)
	case "ME":
		ch := app.CurrentBuffer()
		if ch == "home" {
			return
		}

		line := fmt.Sprintf("\x01ACTION %s\x01", args)
		s.PrivMsg(ch, line)
	default:
	}
}

func formatIRCMessage(nick, content string) (line string) {
	if content == "" {
		line = fmt.Sprintf("\x02%s\x00:", nick)
		return
	}

	if content[0] == 1 {
		content = strings.TrimSuffix(content[1:], "\x01")

		if strings.HasPrefix(content, "ACTION") {
			line = fmt.Sprintf("*\x02%s\x00%s", nick, content[6:])
		} else {
			line = fmt.Sprintf("\x1dCTCP request from\x1d \x02%s\x00: %s", nick, content)
		}

		return
	}

	line = fmt.Sprintf("\x02%s\x00  %s", nick, content)

	return
}
