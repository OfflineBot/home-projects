package automation

import (
	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
)

// What a WLED can play.
//
// The list is the firmware's own, in its own order, because the number is what
// goes over the wire — "66" is Fire 2012 whatever anybody calls it. Kept here
// so a rule can offer them as a choice instead of asking somebody to remember
// a number, and asked for by name in one place so it can later be read from a
// lamp itself without changing anything that uses it.
var wledEffects = []string{
	"Solid", "Blink", "Breathe", "Wipe", "Wipe Random", "Random Colors", "Sweep", "Dynamic",
	"Colorloop", "Rainbow", "Scan", "Scan Dual", "Fade", "Theater", "Theater Rainbow", "Running",
	"Saw", "Twinkle", "Dissolve", "Dissolve Rnd", "Sparkle", "Sparkle Dark", "Sparkle+", "Strobe",
	"Strobe Rainbow", "Strobe Mega", "Blink Rainbow", "Android", "Chase", "Chase Random",
	"Chase Rainbow", "Chase Flash", "Chase Flash Rnd", "Rainbow Runner", "Colorful", "Traffic Light",
	"Sweep Random", "Chase 2", "Aurora", "Stream", "Scanner", "Lighthouse", "Fireworks", "Rain",
	"Tetrix", "Fire Flicker", "Gradient", "Loading", "Rolling Balls", "Fairy", "Two Dots", "Fairytwinkle",
	"Running Dual", "RSVD", "Chase 3", "Tri Wipe", "Tri Fade", "Lightning", "ICU", "Multi Comet",
	"Scanner Dual", "Stream 2", "Oscillate", "Pride 2015", "Juggle", "Palette", "Fire 2012", "Colorwaves",
	"Bpm", "Fill Noise", "Noise 1", "Noise 2", "Noise 3", "Noise 4", "Colortwinkles", "Lake", "Meteor",
	"Meteor Smooth", "Railway", "Ripple", "Twinklefox", "Twinklecat", "Halloween Eyes", "Solid Pattern",
	"Solid Pattern Tri", "Spots", "Spots Fade", "Glitter", "Candle", "Fireworks Starburst",
	"Fireworks 1D", "Bouncing Balls", "Sinelon", "Sinelon Dual", "Sinelon Rainbow", "Popcorn", "Drip",
	"Plasma", "Percent", "Ripple Rainbow", "Heartbeat", "Pacifica", "Candle Multi", "Solid Glitter",
	"Sunrise", "Phased", "Twinkleup", "Noise Pal", "Sine", "Phased Noise", "Flow", "Chunchun",
	"Dancing Shadows", "Washing Machine", "Candy Cane", "Blends", "TV Simulator", "Dynamic Smooth",
}

func mountEffects(r fiber.Router) {
	r.Get("/wled/effects", func(ctx *fiber.Ctx) error {
		if !auth.From(ctx).IsUser() {
			return httpx.Unauthorized("Sign in first.")
		}
		return ctx.JSON(fiber.Map{"effects": wledEffects})
	})
}
