package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"gopkg.in/yaml.v3"
)

// A lamp you can actually press.
//
// A rule that switches the lights on is a fine thing to have, but it is a rule:
// somebody has to write it, and then a second one for switching them off, and
// the board ends up with two buttons that both say what they do rather than
// what the light is doing. This is the other half — a light on a board knows
// whether it is on, and one press changes it.
//
// It talks to WLED directly, the same way the action does, because putting a
// rule in between would only mean the state still could not be read back.

// lightState is what WLED says about itself, in the two words that matter here.
type lightState struct {
	On         bool   `json:"on"`
	Brightness int    `json:"brightness"`
	Colour     string `json:"color,omitempty"`
	Reachable  bool   `json:"reachable"`
}

func wledAddress(host string) string {
	address := strings.TrimSpace(host)
	if address == "" {
		return ""
	}
	if !strings.HasPrefix(address, "http") {
		address = "http://" + address
	}
	return strings.TrimSuffix(address, "/")
}

// firstHost: a card may name several lamps; the state is read from the first
// and every one of them is switched.
func wledHosts(raw string) []string {
	out := []string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if host := strings.TrimSpace(part); host != "" {
			out = append(out, host)
		}
	}
	return out
}

func wledRead(ctx context.Context, host string) (lightState, error) {
	address := wledAddress(host)
	if address == "" {
		return lightState{}, fmt.Errorf("this card has no light yet")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/state", nil)
	if err != nil {
		return lightState{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return lightState{}, fmt.Errorf("%s is not answering", host)
	}
	defer resp.Body.Close()
	var said struct {
		On  bool `json:"on"`
		Bri int  `json:"bri"`
		Seg []struct {
			Col [][]int `json:"col"`
		} `json:"seg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&said); err != nil {
		return lightState{}, fmt.Errorf("%s answered something that is not WLED", host)
	}
	colour := ""
	if len(said.Seg) > 0 && len(said.Seg[0].Col) > 0 && len(said.Seg[0].Col[0]) >= 3 {
		rgb := said.Seg[0].Col[0]
		colour = fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
	}
	return lightState{On: said.On, Brightness: said.Bri, Colour: colour, Reachable: true}, nil
}

func wledWrite(ctx context.Context, host string, state map[string]any) error {
	address := wledAddress(host)
	if address == "" {
		return fmt.Errorf("this card has no light yet")
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/json/state", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s is not answering", host)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s said no (%d)", host, resp.StatusCode)
	}
	return nil
}

// lampsOf resolves what a card carries — a name, or an address — against the
// lamps this project has written down.
func lampsOf(ctx *fiber.Ctx, env *capability.Env, raw string) []string {
	spec, err := Read(ctx.UserContext(), env, capability.Project(ctx))
	hosts := wledHosts(raw)
	if err != nil || spec == nil {
		return hosts
	}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		// Split again after resolving: a name may stand for a whole room, and
		// a room joined back into one string is one address nobody answers to.
		out = append(out, wledHosts(spec.LightAt(host))...)
	}
	return out
}

// A WLED account: several lamps under one name, kept where the other
// connections are kept rather than inside one project's file.
//
// That is what "all the bed lights" and "all the lights" are — a name and a
// list of addresses. Nothing secret is involved, which is why this account has
// no password: what it holds is where the lamps are, not how to get in.
func wledAccountKind() capability.AccountKind {
	return capability.AccountKind{
		Name:        "wled",
		Title:       "Lights (WLED)",
		Description: "One address or twenty under one name — the bed, the desk, or every lamp in the house.",
		Fields: []capability.AccountField{{
			Name: "hosts", Label: "Addresses", Type: "list", Required: true,
			Placeholder: "192.168.178.49",
			Hint:        "One lamp per line. As many as belong together — a bed, a desk, the whole house.",
		}},
		Test: testWLEDAccount,
	}
}

// hostsOfAccount reads the addresses out of a wled account.
func hostsOfAccount(a *model.Account) []string {
	var cfg struct {
		Hosts any `json:"hosts"`
	}
	_ = json.Unmarshal(a.Config, &cfg)
	switch v := cfg.Hosts.(type) {
	case string:
		return wledHosts(v)
	case []any:
		out := []string{}
		for _, one := range v {
			if text, ok := one.(string); ok {
				out = append(out, wledHosts(text)...)
			}
		}
		return out
	}
	return nil
}

// testWLEDAccount asks every lamp whether it is there. One that answers is
// enough to call the account good — a lamp switched off at the wall should not
// make the whole room a fault.
func testWLEDAccount(ctx context.Context, env *capability.Env, a *model.Account, _ []byte) error {
	hosts := hostsOfAccount(a)
	if len(hosts) == 0 {
		return fmt.Errorf("this account has no addresses")
	}
	missing := []string{}
	for _, host := range hosts {
		if _, err := wledRead(ctx, host); err != nil {
			missing = append(missing, host)
		}
	}
	if len(missing) == len(hosts) {
		return fmt.Errorf("none of them answered: %s", strings.Join(missing, ", "))
	}
	return nil
}

// mountLightAccounts: the lamps of an account, switched without a project in
// the middle. A light belongs to the house, not to a project.
func mountLightAccounts(env *capability.Env, r fiber.Router) {
	accountOf := func(ctx *fiber.Ctx) (*model.Account, []string, error) {
		id, err := uuid.Parse(ctx.Params("account"))
		if err != nil {
			return nil, nil, httpx.NotFound("There is no such account.")
		}
		a, err := env.Store.AccountByID(ctx.UserContext(), id)
		if err != nil || a.Kind != "wled" {
			return nil, nil, httpx.NotFound("There is no light account with that id.")
		}
		return a, hostsOfAccount(a), nil
	}

	// Every light account there is, with how many lamps it holds.
	r.Get("/lights", func(ctx *fiber.Ctx) error {
		if !auth.From(ctx).IsUser() {
			return httpx.Unauthorized("Sign in first.")
		}
		all, err := env.Store.ListAccounts(ctx.UserContext())
		if err != nil {
			return httpx.Internal("the accounts could not be read").WithCause(err)
		}
		out := []fiber.Map{}
		for i := range all {
			if all[i].Kind != "wled" {
				continue
			}
			out = append(out, fiber.Map{
				"id": all[i].ID, "title": all[i].Title, "hosts": hostsOfAccount(&all[i]),
			})
		}
		return ctx.JSON(fiber.Map{"lights": out})
	})

	r.Get("/lights/:account", func(ctx *fiber.Ctx) error {
		if !auth.From(ctx).IsUser() {
			return httpx.Unauthorized("Sign in first.")
		}
		a, hosts, err := accountOf(ctx)
		if err != nil {
			return err
		}
		if len(hosts) == 0 {
			return ctx.JSON(fiber.Map{"light": lightState{}, "note": a.Title + " has no addresses"})
		}
		state, rerr := wledRead(ctx.UserContext(), hosts[0])
		if rerr != nil {
			return ctx.JSON(fiber.Map{"light": lightState{}, "note": rerr.Error()})
		}
		return ctx.JSON(fiber.Map{"light": state, "title": a.Title, "lamps": len(hosts)})
	})

	r.Post("/lights/:account", func(ctx *fiber.Ctx) error {
		if !auth.From(ctx).IsUser() {
			return httpx.Unauthorized("Sign in first.")
		}
		a, hosts, err := accountOf(ctx)
		if err != nil {
			return err
		}
		var in lightWish
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("that is not a light to switch")
		}
		state, err := wishToState(in)
		if err != nil {
			return err
		}
		failed := []string{}
		for _, host := range hosts {
			if werr := wledWrite(ctx.UserContext(), host, state); werr != nil {
				failed = append(failed, host)
			}
		}
		if len(failed) == len(hosts) {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error": a.Title + ": none of them answered",
			})
		}
		after, rerr := wledRead(ctx.UserContext(), hosts[0])
		if rerr != nil {
			return ctx.JSON(fiber.Map{"light": lightState{}, "note": rerr.Error()})
		}
		// A lamp that is out is worth saying, but the rest were switched.
		note := ""
		if len(failed) > 0 {
			note = fmt.Sprintf("%d of %d did not answer", len(failed), len(hosts))
		}
		return ctx.JSON(fiber.Map{"light": after, "note": note, "lamps": len(hosts)})
	})
}

// lightWish is what a caller asks of a lamp.
type lightWish struct {
	Host       string `json:"host"`
	Power      string `json:"power"`
	Brightness *int   `json:"brightness"`
	Colour     string `json:"color"`
}

// wishToState turns that into what WLED understands.
func wishToState(in lightWish) (map[string]any, error) {
	state := map[string]any{}
	switch strings.ToLower(strings.TrimSpace(in.Power)) {
	case "on", "true", "1":
		state["on"] = true
	case "off", "false", "0":
		state["on"] = false
	case "toggle":
		state["on"] = "t"
	}
	if in.Brightness != nil {
		n := *in.Brightness
		if n < 0 || n > 255 {
			return nil, httpx.BadRequest("brightness is 0 to 255, not %d", n)
		}
		state["bri"] = n
		if _, said := state["on"]; !said && n > 0 {
			state["on"] = true
		}
	}
	if colour := strings.TrimSpace(in.Colour); colour != "" {
		rgb, err := parseColour(colour)
		if err != nil {
			return nil, httpx.BadRequest("%v", err)
		}
		state["seg"] = []any{map[string]any{"col": []any{rgb}}}
		if _, said := state["on"]; !said {
			state["on"] = true
		}
	}
	if len(state) == 0 {
		return nil, httpx.BadRequest("nothing to change — say on, off, toggle, a brightness or a colour")
	}
	return state, nil
}

// mountLights hangs the routes a light card needs off the project.
func mountLights(env *capability.Env, r fiber.Router) {
	// The lamps this project can reach, by name.
	r.Get("/lights", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		spec, err := Read(ctx.UserContext(), env, capability.Project(ctx))
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		lights := []Light{}
		if spec != nil && spec.Lights != nil {
			lights = spec.Lights
		}
		return ctx.JSON(fiber.Map{"lights": lights})
	})

	// Written down once, used by name everywhere afterwards.
	r.Put("/lights", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var in struct {
			Lights []Light `json:"lights"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The lights could not be read.")
		}
		for i, l := range in.Lights {
			if strings.TrimSpace(l.Name) == "" {
				return httpx.BadRequest("light %d has no name", i+1)
			}
			if len(l.Addresses()) == 0 {
				return httpx.BadRequest("%s has no address", l.Name)
			}
		}
		spec, err := Read(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		if spec == nil {
			spec = &Spec{Rules: []Rule{}}
		}
		spec.Lights = in.Lights
		body, err := yaml.Marshal(spec)
		if err != nil {
			return httpx.Internal("the lights could not be written").WithCause(err)
		}
		author, email := capability.AuthorOf(ctx)
		if _, err := env.Files.Write(ctx.UserContext(), p, File, body, files.Op{
			Author: author, Email: email, Message: "Edit lights", Commit: true,
		}); err != nil {
			return err
		}
		return ctx.JSON(fiber.Map{"lights": spec.Lights})
	})

	// What is it doing right now — so the button can say "on" rather than guess.
	r.Get("/light", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		hosts := lampsOf(ctx, env, ctx.Query("host"))
		if len(hosts) == 0 {
			return httpx.BadRequest("Which light? Give this card an address.")
		}
		state, err := wledRead(ctx.UserContext(), hosts[0])
		if err != nil {
			// A lamp that is unplugged is not an error the page should break on;
			// the card says "not answering" and stays a button.
			return ctx.JSON(fiber.Map{"light": lightState{}, "note": err.Error()})
		}
		return ctx.JSON(fiber.Map{"light": state})
	})

	// On, off, the other one, or a brightness. Writing, so a read-only visitor
	// and a read token cannot press it.
	r.Post("/light", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		var in struct {
			Host       string `json:"host"`
			Power      string `json:"power"`
			Brightness *int   `json:"brightness"`
			Colour     string `json:"color"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("that is not a light to switch")
		}
		hosts := lampsOf(ctx, env, in.Host)
		if len(hosts) == 0 {
			return httpx.BadRequest("Which light? Give this card an address.")
		}
		state := map[string]any{}
		switch strings.ToLower(strings.TrimSpace(in.Power)) {
		case "on", "true", "1":
			state["on"] = true
		case "off", "false", "0":
			state["on"] = false
		case "toggle":
			state["on"] = "t" // WLED's own word for "the other one"
		}
		if in.Brightness != nil {
			n := *in.Brightness
			if n < 0 || n > 255 {
				return httpx.BadRequest("brightness is 0 to 255, not %d", n)
			}
			state["bri"] = n
			if _, said := state["on"]; !said && n > 0 {
				state["on"] = true
			}
		}
		if colour := strings.TrimSpace(in.Colour); colour != "" {
			rgb, err := parseColour(colour)
			if err != nil {
				return httpx.BadRequest("%v", err)
			}
			state["seg"] = []any{map[string]any{"col": []any{rgb}}}
			// A colour asked for while it is off means: on, in that colour.
			if _, said := state["on"]; !said {
				state["on"] = true
			}
		}
		if len(state) == 0 {
			return httpx.BadRequest("nothing to change — say on, off, toggle, a brightness or a colour")
		}
		for _, host := range hosts {
			if err := wledWrite(ctx.UserContext(), host, state); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
			}
		}
		after, err := wledRead(ctx.UserContext(), hosts[0])
		if err != nil {
			return ctx.JSON(fiber.Map{"light": lightState{}, "note": err.Error()})
		}
		return ctx.JSON(fiber.Map{"light": after})
	})
}
