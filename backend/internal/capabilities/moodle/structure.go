package moodle

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	lib "github.com/OfflineBot/nicht-libs/moodle"
	"github.com/offlinebot/home-projects/backend/internal/slug"
)

// Moodle already has a shape: a course is made of sections, a section holds
// activities, and one kind of activity is a folder with a tree inside it. The
// library flattens all of that into a list of files, which is why 51 lecture
// slides arrived as 51 loose files. This reads the same answer and keeps the
// shape.
type moodleSection struct {
	Name    string         `json:"name"`
	Section int            `json:"section"`
	Modules []moodleModule `json:"modules"`
}

type moodleModule struct {
	Name     string          `json:"name"`
	ModName  string          `json:"modname"`
	Contents []moodleContent `json:"contents"`
}

type moodleContent struct {
	Type     string `json:"type"`
	Filename string `json:"filename"`
	// Filepath is the path *inside* a folder activity, e.g. "/Woche 3/".
	Filepath string `json:"filepath"`
	Filesize int    `json:"filesize"`
	Fileurl  string `json:"fileurl"`
}

// item is one file with the place it belongs in.
type item struct {
	// Rel is the path under the course folder, sections and all.
	Rel      string
	Filename string
	Fileurl  string
	Filesize int
}

// courseTree lists a course's files with the structure Moodle gives them.
//
// keepShape false returns them as one flat list — the same thing the library
// did — for whoever wants exactly that.
func courseTree(baseURL, token string, courseID int, keepShape bool) ([]item, error) {
	body, err := lib.CallAPI(baseURL, token, "core_course_get_contents", url.Values{
		"courseid": {strconv.Itoa(courseID)},
	})
	if err != nil {
		return nil, err
	}
	var sections []moodleSection
	if err := json.Unmarshal(body, &sections); err != nil {
		return nil, fmt.Errorf("the course contents could not be read: %w", err)
	}

	var out []item
	for i, section := range sections {
		sectionDir := ""
		if keepShape {
			sectionDir = folderName(section.Name)
			if sectionDir == "" {
				sectionDir = fmt.Sprintf("%02d-abschnitt", section.Section)
			}
			// Moodle's first section is the course's own front page and has no
			// name worth a folder of its own.
			if i == 0 && strings.TrimSpace(section.Name) == "" {
				sectionDir = ""
			}
		}
		for _, mod := range section.Modules {
			// A folder activity *is* a folder. Everything else is one file
			// sitting in its section, and giving each of those a folder of its
			// own would only add a level to click through.
			modDir := ""
			if keepShape && mod.ModName == "folder" {
				modDir = folderName(mod.Name)
			}
			for _, content := range mod.Contents {
				if content.Filename == "" || content.Fileurl == "" {
					continue
				}
				dir := path.Join(sectionDir, modDir)
				if keepShape {
					// The tree inside a folder activity travels with it.
					for _, part := range strings.Split(strings.Trim(content.Filepath, "/"), "/") {
						if part != "" {
							dir = path.Join(dir, folderName(part))
						}
					}
				}
				out = append(out, item{
					Rel:      path.Join(dir, sanitise(content.Filename)),
					Filename: content.Filename,
					Fileurl:  content.Fileurl,
					Filesize: content.Filesize,
				})
			}
		}
	}
	return out, nil
}

// folderName makes a section or activity name usable as a folder without
// turning it into an unreadable slug — spaces and umlauts are fine in a
// filesystem, slashes and dots at the front are not.
func folderName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\n", " ", "\t", " ").Replace(name)
	name = strings.Trim(name, ". ")
	if len(name) > 80 {
		name = strings.TrimSpace(name[:80])
	}
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// ------------------------------------------------------------------- routing

// route says which project a course belongs in. It exists because "everything
// from Moodle" is not one thing: the second semester and the third are two
// different collections, and they want to be two different projects.
type route struct {
	match    string // as typed, for the log
	semester int    // >0 when the line was a bare number
	text     string // lower-cased needle for a name match
	all      bool   // the * line
	project  string
}

// parseRoutes reads the routing table. One rule per line, and the first rule
// that matches wins:
//
//	2 -> semester-2                 a bare number is the semester Moodle derives
//	WDS125 -> wissenschaftliches    anything else matches the course name
//	* -> moodle-archiv              the catch-all, however far down it is
//
// A line without an arrow is not a rule and is reported rather than ignored.
func parseRoutes(text string) ([]route, []string) {
	var routes []route
	var bad []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		left, right, ok := strings.Cut(line, "->")
		if !ok {
			left, right, ok = strings.Cut(line, ":")
		}
		if !ok {
			bad = append(bad, line)
			continue
		}
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		if left == "" || right == "" {
			bad = append(bad, line)
			continue
		}
		r := route{match: left, project: right}
		switch {
		case left == "*":
			r.all = true
		default:
			if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(left), "semester")); err == nil {
				r.semester = n
			} else {
				r.text = strings.ToLower(left)
			}
		}
		routes = append(routes, r)
	}
	return routes, bad
}

// pick returns the project slug for a course, and whether any rule matched.
func pick(routes []route, course lib.Course) (string, bool) {
	name := strings.ToLower(course.Shortname + " " + course.Fullname + " " + course.CategoryName)
	for _, r := range routes {
		switch {
		case r.all:
			return r.project, true
		case r.semester > 0:
			if course.SemesterNumber == r.semester {
				return r.project, true
			}
		case r.text != "" && strings.Contains(name, r.text):
			return r.project, true
		}
	}
	return "", false
}

// courseFolder is the name a course gets when it becomes a folder.
func courseFolder(course lib.Course) string {
	name := course.Shortname
	if name == "" {
		name = course.Fullname
	}
	return slug.Make(name)
}
