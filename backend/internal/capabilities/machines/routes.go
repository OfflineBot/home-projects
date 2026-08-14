package machines

import (
	"context"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// What the page asks for.
//
// Everything that needs a sign-in is a POST with the password in the body, not
// a GET with it in the address — an address ends up in logs, in history and in
// the referrer of the next request, and a password does not belong in any of
// those. The password is used for that one connection and then it is gone; it
// is not cached, not written to the project, and not put in a session.
//
// How long it lives is the browser's business, and the page offers the two
// answers a person actually wants: keep it while I am here, or ask me every
// time.

type withPassword struct {
	Password string `json:"password"`
}

// Routes are mounted under /api/projects/:project/machines
func (c Capability) Routes(env *capability.Env, r fiber.Router) {
	c.mountPTY(env, r)

	// The list, and whether each one is on. The status is asked for here
	// rather than by the page one machine at a time, because "is it on" is the
	// first thing the page is opened for.
	r.Get("/", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		list := read(ctx.UserContext(), env, p)
		type state struct {
			Machine
			Up bool `json:"up"`
		}
		out := make([]state, len(list.Machines))
		for i, m := range list.Machines {
			filled := resolve(ctx.UserContext(), env, m)
			out[i] = state{Machine: filled, Up: filled.Host != "" && filled.up(ctx.UserContext())}
		}
		return ctx.JSON(fiber.Map{"machines": out})
	})

	// Adding, changing and removing a machine is editing the file, and the file
	// is the truth. Doing it through here means the shape stays right.
	r.Put("/", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in List
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The machines could not be read.")
		}
		for i := range in.Machines {
			in.Machines[i].Name = strings.TrimSpace(in.Machines[i].Name)
			in.Machines[i].Host = strings.TrimSpace(in.Machines[i].Host)
			if in.Machines[i].Name == "" {
				return httpx.BadRequest("A machine needs a name.")
			}
			// An address is only needed when no account carries one.
			if in.Machines[i].Host == "" && in.Machines[i].Account == "" {
				return httpx.BadRequest("%s needs an address, or an account that has one.",
					in.Machines[i].Name)
			}
		}
		author, email := capability.AuthorOf(ctx)
		if err := write(ctx.UserContext(), env, capability.Project(ctx), in, author, email); err != nil {
			return httpx.Internal("the machines could not be saved").WithCause(err)
		}
		return ctx.JSON(in)
	})

	// Wake needs no credential at all: the magic packet is not a sign-in, it is
	// a shout on the network.
	r.Post("/:name/wake", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		if m.MAC == "" {
			return httpx.BadRequest("%s has no MAC address, so it cannot be woken.", m.Name)
		}
		if err := wake(m); err != nil {
			return httpx.BadRequest("%v", err)
		}
		return ctx.JSON(fiber.Map{"sent": true,
			"message": "The packet is on its way. It takes a moment before it answers."})
	})

	// Off, and back on. Both are one command over SSH, which is why both need
	// the sign-in.
	r.Post("/:name/power", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in struct {
			withPassword
			What string `json:"what"` // shutdown | reboot
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		command := "sudo -n shutdown -h now"
		if in.What == "reboot" {
			command = "sudo -n shutdown -r now"
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()
		text, rerr := run(client, command)
		if rerr != nil && strings.TrimSpace(text) != "" {
			// A machine that is going down often cuts the connection mid-word.
			// That is not a failure, and saying it is would train people to
			// ignore the message.
			if !strings.Contains(text, "password") && !strings.Contains(text, "sudo:") {
				return ctx.JSON(fiber.Map{"output": text})
			}
			return httpx.New(fiber.StatusBadGateway, "command_failed", "%s", strings.TrimSpace(text))
		}
		return ctx.JSON(fiber.Map{"output": text})
	})

	// The sessions on that machine. tmux prints one line each; the format is
	// asked for explicitly so it does not change under us.
	r.Post("/:name/tmux", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		var in withPassword
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()

		// Measured, not assumed: tmux turns a tab in its format output into an
		// underscore, so the fields are separated by a bar. A session name may
		// contain one, so the line is read from the right, where the three
		// known fields are.
		text, rerr := run(client, "tmux list-sessions -F "+
			"'#{session_name}|#{session_windows}|#{session_attached}|#{t:session_created}' 2>&1 || true")
		type session struct {
			Name     string `json:"name"`
			Windows  string `json:"windows"`
			Attached bool   `json:"attached"`
			Created  string `json:"created"`
		}
		out := []session{}
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			parts := strings.Split(line, "|")
			if len(parts) < 4 {
				continue
			}
			tail := parts[len(parts)-3:]
			out = append(out, session{
				Name:     strings.Join(parts[:len(parts)-3], "|"),
				Windows:  tail[0],
				Attached: tail[1] == "1",
				Created:  tail[2],
			})
		}
		answer := fiber.Map{"sessions": out}
		if len(out) == 0 {
			// tmux says "no server running on …" when there is nothing, which
			// is worth passing on as it is rather than as an empty list.
			answer["note"] = strings.TrimSpace(text)
			if rerr != nil && strings.TrimSpace(text) == "" {
				answer["note"] = "tmux is not installed on " + m.Name
			}
		}
		return ctx.JSON(answer)
	})

	// Looking into a session: what is on its screen right now.
	r.Post("/:name/tmux/:session", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		var in struct {
			withPassword
			Lines int `json:"lines"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if in.Lines <= 0 || in.Lines > 5000 {
			in.Lines = 200
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()
		name := shellQuote(sessionOf(ctx))
		text, _ := run(client, fmt.Sprintf("tmux capture-pane -p -S -%d -t %s 2>&1", in.Lines, name))
		return ctx.JSON(fiber.Map{"screen": text})
	})

	// Typing into it. This is the whole of "and do things there": the keys go
	// to the session, the session does what it always does, and the next look
	// shows the result.
	r.Post("/:name/tmux/:session/keys", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in struct {
			withPassword
			Keys string `json:"keys"`
			// Enter says whether the line is sent off or only typed.
			Enter bool `json:"enter"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()
		name := shellQuote(sessionOf(ctx))
		command := fmt.Sprintf("tmux send-keys -t %s %s", name, shellQuote(in.Keys))
		if in.Enter {
			command += " Enter"
		}
		if text, rerr := run(client, command+" 2>&1"); rerr != nil {
			return httpx.New(fiber.StatusBadGateway, "command_failed", "%s", strings.TrimSpace(text))
		}
		// The screen right after, so the page does not have to ask twice.
		screen, _ := run(client, fmt.Sprintf("sleep 0.4; tmux capture-pane -p -S -200 -t %s 2>&1", name))
		return ctx.JSON(fiber.Map{"screen": screen})
	})

	// A session of one's own, and closing one.
	r.Post("/:name/tmux-new", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in struct {
			withPassword
			Session string `json:"session"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if strings.TrimSpace(in.Session) == "" {
			return httpx.BadRequest("A session needs a name.")
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()
		text, rerr := run(client, "tmux new-session -d -s "+shellQuote(in.Session)+" 2>&1")
		if rerr != nil {
			return httpx.New(fiber.StatusBadGateway, "command_failed", "%s", strings.TrimSpace(text))
		}
		return ctx.JSON(fiber.Map{"created": in.Session})
	})

	r.Post("/:name/tmux/:session/kill", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in withPassword
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		m, err := machineOf(ctx, env)
		if err != nil {
			return err
		}
		client, cerr := c.signIn(ctx.UserContext(), env, m, in.Password)
		if cerr != nil {
			return httpx.New(fiber.StatusBadGateway, "ssh_failed", "%v", cerr)
		}
		defer client.Close()
		text, rerr := run(client, "tmux kill-session -t "+shellQuote(sessionOf(ctx))+" 2>&1")
		if rerr != nil {
			return httpx.New(fiber.StatusBadGateway, "command_failed", "%s", strings.TrimSpace(text))
		}
		return httpx.OK(ctx)
	})
}

func machineOf(ctx *fiber.Ctx, env *capability.Env) (Machine, error) {
	p := capability.Project(ctx)
	list := read(ctx.UserContext(), env, p)
	name := ctx.Params("name")
	m, ok := find(list, name)
	if !ok {
		return Machine{}, httpx.NotFound("There is no machine called %q here.", name)
	}
	return resolve(ctx.UserContext(), env, m), nil
}

// shellQuote makes one argument out of whatever was typed. Everything that goes
// to the other machine goes through here: a session name is a name, not a place
// to hide a second command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Exports are what the rest of the server can use: a tile on the dashboard that
// says whether the machine is on, without this capability being named anywhere
// in the core.
func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	list := read(ctx, env, p)
	out := []store.VariableInput{
		{Name: "machines", Type: "number", Value: len(list.Machines), Source: "capability:machines"},
	}
	for _, m := range list.Machines {
		out = append(out, store.VariableInput{
			Name: slug.Make(m.Name) + "_up", Type: "bool", Value: m.up(ctx),
			Source: "capability:machines", History: true,
		})
	}
	return out, nil
}

// sessionOf is the session name as it was typed. The server decodes the path
// itself, so nothing is undone twice here — a session called "a+b" is a+b.
func sessionOf(ctx *fiber.Ctx) string { return ctx.Params("session") }
