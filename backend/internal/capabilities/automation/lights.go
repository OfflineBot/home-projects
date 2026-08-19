package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
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
		out = append(out, spec.LightAt(host))
	}
	return out
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
			if strings.TrimSpace(l.Host) == "" {
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
