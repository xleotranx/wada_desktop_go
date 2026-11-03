package video

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// setClipTiming writes a clip's start and duration into its composition file,
// changing only those attribute values: the rest of the file, and the way the
// agent wrote it, stays as it is.
func setClipTiming(src, id string, start, duration float64) (string, error) {
	return setClipTimingOn(src, id, start, duration, -1)
}

// setClipTimingOn writes a clip's timing on a track; a negative track keeps
// the lane it already has.
func setClipTimingOn(src, id string, start, duration float64, track int) (string, error) {
	doc, err := scanHTML(src)
	if err != nil {
		return "", err
	}
	el := doc.byID(id)
	if el == nil {
		return "", fmt.Errorf("no element with id %q", id)
	}
	values := map[string]string{
		"data-start":    formatSeconds(start),
		"data-duration": formatSeconds(duration),
	}
	if track >= 0 {
		values["data-track-index"] = strconv.Itoa(track)
	}
	return setAttrs(src, el, values)
}

// setRootDuration writes the composition's length on the root element.
func setRootDuration(src, id string, seconds float64) (string, error) {
	doc, err := scanHTML(src)
	if err != nil {
		return "", err
	}
	el := doc.byID(id)
	if el == nil {
		return "", fmt.Errorf("no element with id %q", id)
	}
	return setAttrs(src, el, map[string]string{"data-duration": formatSeconds(seconds)})
}

// change replaces a byte range with text; an empty range inserts.
type change struct {
	span
	text string
}

// setAttrs replaces or adds attribute values on one element and returns the
// file. Values are quoted; an attribute the element lacks is added before the
// tag's closing bracket.
func setAttrs(src string, el *element, values map[string]string) (string, error) {
	return applyChanges(src, attrChanges(src, el, values))
}

// attrChanges is what writing double-quoted values onto an element changes.
func attrChanges(src string, el *element, values map[string]string) []change {
	return attrChangesQuoted(src, el, values, `"`)
}

// attrChangesQuoted is attrChanges with a choice of quotes, for a value that
// already holds quotes of its own.
func attrChangesQuoted(src string, el *element, values map[string]string, quote string) []change {
	var changes []change
	for name, value := range values {
		if a, ok := el.attr(name); ok {
			q := quote
			if quote == `"` && a.Quoted && a.ValueAt.Start > 0 && src[a.ValueAt.Start-1] == '\'' {
				// Keep the file's own style for ordinary edits.
				q = "'"
			}
			quoted := q + value + q
			if a.Quoted {
				changes = append(changes, change{span{a.ValueAt.Start - 1, a.ValueAt.End + 1}, quoted})
			} else {
				changes = append(changes, change{a.ValueAt, quoted})
			}
			continue
		}
		insertAt := el.Opening.End - 1
		if insertAt > 0 && src[insertAt-1] == '/' {
			insertAt--
		}
		changes = append(changes, change{span{insertAt, insertAt}, " " + name + "=" + quote + value + quote})
	}
	return changes
}

// applyChanges writes changes into a file, from its end so earlier offsets
// stay where they were.
func applyChanges(src string, changes []change) (string, error) {
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Start > changes[j].Start })
	out := src
	for _, c := range changes {
		if c.Start < 0 || c.End > len(out) || c.Start > c.End {
			return "", errors.New("the file changed while it was editing")
		}
		out = out[:c.Start] + c.text + out[c.End:]
	}
	return out, nil
}

// addScene appends a scene of length seconds to the composition and returns
// its id: a new file under compositions/, and a host at the end of the root.
func addScene(dir string, project *Project, length float64) (string, error) {
	if project == nil || project.Root == nil || project.Root.ID == "" {
		return "", errors.New("the composition has no root")
	}
	index := projectFile(dir, project.File)
	data, err := os.ReadFile(index)
	if err != nil {
		return "", err
	}
	doc, err := scanHTML(string(data))
	if err != nil {
		return "", err
	}
	root := doc.byID(project.Root.ID)
	if root == nil {
		return "", errors.New("the root element is gone")
	}
	if root.Closing.Start == 0 {
		return "", errors.New("the root element has no closing tag")
	}
	id := ""
	number := len(project.Scenes) + 1
	for ; ; number++ {
		candidate := fmt.Sprintf("scene-%02d", number)
		if _, err := os.Stat(filepath.Join(dir, "compositions", candidate+".html")); errors.Is(err, fs.ErrNotExist) {
			id = candidate
			break
		}
	}
	file := "compositions/" + id + ".html"
	title := fmt.Sprintf("Scene %d", number)
	if err := writeFileAtomic(projectFile(dir, file), []byte(sceneTemplate(id, title, length)), 0o644); err != nil {
		return "", err
	}
	start := project.Duration()
	host := fmt.Sprintf("\n      <div\n        id=\"%s\"\n        data-composition-id=\"%s\"\n        data-composition-src=\"%s\"\n        data-start=\"%s\"\n        data-duration=\"%s\"\n        data-track-index=\"0\"\n      ></div>\n    ",
		id, id, file, formatSeconds(start), formatSeconds(length))
	out := string(data[:root.Inner.End]) + host + string(data[root.Inner.End:])
	if project.Root.HasDuration && start+length > project.Root.DeclaredSeconds {
		if extended, err := setRootDuration(out, project.Root.ID, start+length); err == nil {
			out = extended
		}
	}
	if err := writeFileAtomic(index, []byte(out), 0o644); err != nil {
		return "", err
	}
	return id, nil
}

// formatSeconds writes a time without trailing zeros: "2", "0.5".
func formatSeconds(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// writeFileAtomic writes a file through a temporary one beside it, so a crash
// leaves either the old file or the new one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".video-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
