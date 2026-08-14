package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/slug"
)

// More than one person on the server.
//
// Anyone may ask for an account. Nobody gets one until the admin says so — and
// until then the account opens nothing, which is said at the sign-in rather
// than discovered page by page. What each person makes is theirs; the admin
// sees everything, because someone has to be able to.

func (s *Server) mountUsers(r fiber.Router) {
	// Asking for an account. Open by necessity: the person asking has no
	// account yet. Throttled by address, and it hands out nothing — not even
	// whether the name was taken, beyond the plain fact that it is.
	r.Post("/auth/register", func(c *fiber.Ctx) error {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Note     string `json:"note"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		name := slug.Make(strings.TrimSpace(in.Username))
		if len(name) < 3 {
			return httpx.BadRequest("A user name needs at least three usable characters.")
		}
		if len(in.Password) < 10 {
			return httpx.BadRequest("A password of at least ten characters, please.")
		}
		ctx := c.UserContext()
		ip := auth.ClientIP(c)
		// What is worth limiting is accounts actually made, not typos: a taken
		// name or a short password costs nothing and is not counted below.
		if fails, _ := s.Store.RecentFailures(ctx, "register", ip, 15*time.Minute); fails >= 5 {
			return httpx.TooMany("That is enough for now. Try again later.")
		}

		if _, err := s.Store.UserByName(ctx, name); err == nil {
			return httpx.Conflict("That name is taken.")
		}
		hash, err := secret.Hash(in.Password)
		if err != nil {
			return httpx.Internal("the password could not be stored").WithCause(err)
		}
		user, err := s.Store.CreateUser(ctx, name, hash, name, false)
		if err != nil {
			return httpx.Conflict("That name is taken.")
		}
		s.Store.RecordAttempt(ctx, "register", ip, ip, false)
		if strings.TrimSpace(in.Note) != "" {
			_, _ = s.Store.SetUserNote(ctx, user.ID, strings.TrimSpace(in.Note))
		}
		s.Store.Audit(ctx, nil, "user.asked", name, ip, nil)
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"waiting": true,
			"message": "The account is made. It opens once it has been let in.",
		})
	})

	g := r.Group("/users", requireAdmin)

	g.Get("/", func(c *fiber.Ctx) error {
		list, err := s.Store.ListUsers(c.UserContext())
		if err != nil {
			return httpx.Internal("the users could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"users": list})
	})

	g.Post("/:id/approve", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a user id.")
		}
		if err := s.stepUp(c, "letting someone in"); err != nil {
			return err
		}
		approved := !c.QueryBool("undo", false)
		user, err := s.Store.ApproveUser(c.UserContext(), id, approved)
		if err != nil {
			return httpx.NotFound("There is no such account.")
		}
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(),
			map[bool]string{true: "user.approved", false: "user.suspended"}[approved],
			user.Username, auth.ClientIP(c), nil)
		return c.JSON(user)
	})

	g.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a user id.")
		}
		if err := s.stepUp(c, "removing an account"); err != nil {
			return err
		}
		if err := s.Store.DeleteUser(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such account, or it is the owner's.")
		}
		return httpx.OK(c)
	})
}

// requireAdmin is for the pages only the owner has.
func requireAdmin(c *fiber.Ctx) error {
	if !auth.From(c).IsAdmin() {
		return httpx.Forbidden("Only the owner can do that.")
	}
	return c.Next()
}
