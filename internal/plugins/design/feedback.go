package design

import "slices"

const feedbackKey = "feedback-v1"

// Feedback belongs to a design folder even before its first message creates a session.
type savedFeedback struct {
	Origin string
	Folder string
	Marks  []*mark
}

func (p *Plugin) loadMarks() {
	p.marks = nil
	p.nextID = 0
	for _, f := range p.feedback {
		if f.Origin == p.origin && f.Folder == p.folder {
			p.marks = cloneMarks(f.Marks)
			for _, m := range p.marks {
				p.nextID = max(p.nextID, m.ID)
			}
			return
		}
	}
}

func cloneMarks(marks []*mark) []*mark {
	out := make([]*mark, 0, len(marks))
	for _, m := range marks {
		if m == nil || m.Words == "" {
			continue
		}
		copy := *m
		out = append(out, &copy)
	}
	return out
}

func (p *Plugin) saveMarks() {
	if p.folder == "" {
		return
	}
	p.feedback = slices.DeleteFunc(p.feedback, func(f savedFeedback) bool { return f.Origin == p.origin && f.Folder == p.folder })
	if marks := cloneMarks(p.marks); len(marks) > 0 {
		p.feedback = append(p.feedback, savedFeedback{Origin: p.origin, Folder: p.folder, Marks: marks})
	}
	p.ctx.Settings().Set(feedbackKey, p.feedback)
}
