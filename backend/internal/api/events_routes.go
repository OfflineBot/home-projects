package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/valyala/fasthttp"
)

// The browser is told what happened, instead of asking again and again.
//
// Inside the server the parts already talk this way: a file is written, a
// variable takes a new value, a scheduler finishes, and whoever cares hears
// about it. This hands the same events to the page, so a number on a board is
// the number as it is now rather than as it was when the tab was opened.
//
// What it is not: a second API. No state travels here — an event says what
// happened and the page asks for the part that changed. That keeps this one
// route from turning into a copy of every other one.
//
// Two rules it keeps, both learned the hard way elsewhere:
//
//   - A slow reader is dropped, never waited for. The channel has room for a
//     handful of events and a full one throws the newest away; a browser on a
//     bad connection must not be able to hold up a file write.
//   - Nobody hears about a project they may not read. The check is the same
//     one every other route uses, and the answer is remembered per connection
//     so a busy project does not mean a database query per event.
func (s *Server) mountEvents(r fiber.Router) {
	r.Get("/events", func(c *fiber.Ctx) error {
		actor := auth.From(c)
		if !actor.IsUser() {
			// A token belongs to a machine that asks for what it needs; the
			// stream is for a person with a page open.
			return httpx.Unauthorized("Sign in first.")
		}

		c.Set(fiber.HeaderContentType, "text/event-stream")
		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Set(fiber.HeaderConnection, "keep-alive")
		// nginx buffers by default, which turns a live stream into a very late
		// one. This is the header that tells it not to.
		c.Set("X-Accel-Buffering", "no")

		feed := make(chan events.Event, 32)
		stop := s.Env.Bus.Subscribe(func(e events.Event) {
			select {
			case feed <- e:
			default:
			}
		})

		mayHear := s.hearing(actor)

		c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
			defer stop()
			// Said at once, so the page knows it is connected rather than
			// waiting for the first thing to happen.
			if _, err := fmt.Fprint(w, ": open\n\n"); err != nil {
				return
			}
			if err := w.Flush(); err != nil {
				return
			}
			// A comment every half minute keeps proxies from closing a quiet
			// stream, and tells us when the other end is gone.
			beat := time.NewTicker(30 * time.Second)
			defer beat.Stop()
			for {
				select {
				case e := <-feed:
					if !mayHear(e) {
						continue
					}
					body, err := json.Marshal(e)
					if err != nil {
						continue
					}
					// No event name: the kind is in the message, and a page
					// that has to register a listener per kind stops working
					// the day a new kind is published.
					if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				case <-beat.C:
					if _, err := fmt.Fprint(w, ": still here\n\n"); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				}
			}
		}))
		return nil
	})
}

// hearing decides, once per project, whether this person may be told about it.
func (s *Server) hearing(actor *auth.Actor) func(events.Event) bool {
	known := map[uuid.UUID]bool{}
	return func(e events.Event) bool {
		if e.ProjectID == uuid.Nil {
			// Not about one project — the owner hears it, nobody else does.
			return actor.IsAdmin()
		}
		if allowed, seen := known[e.ProjectID]; seen {
			return allowed
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		p, err := s.Env.Store.ProjectByID(ctx, e.ProjectID)
		allowed := err == nil && access.RequireReadProject(actor, p) == nil
		known[e.ProjectID] = allowed
		return allowed
	}
}
