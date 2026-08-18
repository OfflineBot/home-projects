package machines

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"golang.org/x/crypto/ssh"
)

// A real terminal, over one socket.
//
// Reading a pane and sending keys was enough to look at something; it is not
// enough to work. This is the other thing: an SSH session with a pty on the
// other end, tmux attached to it, and the bytes going both ways as they come.
// Colours, the cursor, vim, less, everything — because none of it is being
// interpreted here.
//
// Two rules it keeps:
//
//   - The password never appears in an address. A browser cannot set headers on
//     a WebSocket, so the first message on the socket is the sign-in and
//     nothing is sent before it.
//   - It always lands in tmux: `new-session -A` attaches to the session if it
//     is there and starts it if it is not. Closing the tab leaves the work
//     running, which is the whole reason tmux is in the middle.
func (c Capability) mountPTY(env *capability.Env, r fiber.Router) {
	r.Use("/:name/pty", func(ctx *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(ctx) {
			return fiber.ErrUpgradeRequired
		}
		// The machine is looked up before the socket opens: a bad name should
		// be an ordinary error, not a socket that closes without a word.
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		ctx.Locals("machine", m)
		ctx.Locals("session", strings.TrimSpace(ctx.Query("session")))
		return ctx.Next()
	})

	r.Get("/:name/pty", websocket.New(func(conn *websocket.Conn) {
		defer conn.Close()
		m, _ := conn.Locals("machine").(Machine)
		session, _ := conn.Locals("session").(string)
		if session == "" {
			session = "main"
		}

		// The first message is the sign-in. An account means nothing is asked.
		password := ""
		if m.Account == "" {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
			_, first, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var hello struct {
				Password string `json:"password"`
			}
			_ = json.Unmarshal(first, &hello)
			password = hello.Password
		}
		_ = conn.SetReadDeadline(time.Time{})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client, err := c.signIn(ctx, env, m, password)
		if err != nil {
			say(conn, "\r\n"+err.Error()+"\r\n")
			return
		}
		defer client.Close()

		ssession, err := client.NewSession()
		if err != nil {
			say(conn, "\r\n"+err.Error()+"\r\n")
			return
		}
		defer ssession.Close()

		if err := ssession.RequestPty("xterm-256color", 30, 110, ssh.TerminalModes{
			ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400,
		}); err != nil {
			say(conn, "\r\nno terminal on the other side: "+err.Error()+"\r\n")
			return
		}

		in, err := ssession.StdinPipe()
		if err != nil {
			return
		}
		out, err := ssession.StdoutPipe()
		if err != nil {
			return
		}
		errOut, _ := ssession.StderrPipe()

		// Always tmux: attach if it is there, start it if it is not.
		//
		// Attached with -d, and with the window following this client. A tmux
		// whose window-size is anything but "latest" keeps the session as narrow
		// as another client that is still attached — a session left open in an
		// 80-column shell stays 80 columns wide in a browser window twice that,
		// with the rest of the screen simply empty.
		name := shellQuote(session)
		command := "tmux new-session -A -d -s " + name +
			"; tmux set-option -t " + name + " window-size latest >/dev/null 2>&1" +
			"; tmux attach-session -d -t " + name
		if err := ssession.Start(command); err != nil {
			say(conn, "\r\ntmux could not be started: "+err.Error()+"\r\n")
			return
		}

		go pump(conn, out)
		if errOut != nil {
			go pump(conn, errOut)
		}

		for {
			kind, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			// Binary is what was typed; text is a word about the terminal
			// itself — so far only its size.
			if kind == websocket.TextMessage && len(message) > 0 && message[0] == '{' {
				var control struct {
					Cols int `json:"cols"`
					Rows int `json:"rows"`
				}
				if json.Unmarshal(message, &control) == nil && control.Cols > 0 && control.Rows > 0 {
					_ = ssession.WindowChange(control.Rows, control.Cols)
					continue
				}
			}
			if _, err := in.Write(message); err != nil {
				break
			}
		}
		_ = ssession.Signal(ssh.SIGHUP)
	}))
}

func pump(conn *websocket.Conn, from io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := from.Read(buf)
		if n > 0 {
			if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func say(conn *websocket.Conn, text string) {
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte(text))
}
