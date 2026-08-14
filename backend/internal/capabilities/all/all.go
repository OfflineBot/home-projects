// Package all is the registry. One line per capability, nothing else.
//
// This is the whole of what "plug and play" means here: adding a capability is
// a folder plus a line below. Deleting one is deleting that folder and its
// line — and the server still builds and runs. Projects that used it then
// simply show their files.
package all

import (
	"github.com/offlinebot/home-projects/backend/internal/capabilities/automation"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/feed"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/grades"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/machines"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/mail"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/markdown"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/moodle"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/site"
	"github.com/offlinebot/home-projects/backend/internal/capability"
)

// Register makes the built-in capabilities known.
func Register() {
	capability.Register(calendar.New())
	capability.Register(markdown.New())
	capability.Register(grades.New())
	capability.Register(site.New())
	capability.Register(mail.New())
	capability.Register(feed.New())
	capability.Register(automation.New())
	capability.Register(moodle.New())
	capability.Register(machines.New())
}
