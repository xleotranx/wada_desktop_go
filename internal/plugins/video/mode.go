package video

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// videoIndex finds the conversation's video under the backend origin it was
// made in.
func (p *Plugin) videoIndex(s agent.SessionID) int {
	origin := p.agent.Status().Origin
	for i, v := range p.videos {
		if v.Session == s && v.Origin == origin {
			return i
		}
	}
	return -1
}

// videoDir is the project folder of the video the conversation is: a new
// chat made with New Video, or a conversation one became; empty for others.
func (p *Plugin) videoDir(conv chat.Conversation) string {
	if conv.Session == "" {
		if p.draft {
			return p.draftDir
		}
		return ""
	}
	if i := p.videoIndex(conv.Session); i >= 0 {
		return p.videos[i].Dir
	}
	return ""
}

// remember records a conversation as a video.
func (p *Plugin) remember(v video) {
	if v.Session == "" || v.Dir == "" {
		return
	}
	if i := p.videoIndex(v.Session); i >= 0 {
		p.videos[i].Dir = v.Dir
	} else {
		p.videos = append(p.videos, v)
	}
	p.saveVideos()
}

// saveVideos keeps the videos in the settings.
func (p *Plugin) saveVideos() { p.ctx.Settings().Set(videosKey, p.videos) }

// canVideo reports that a new video can start: the agent is ready and no
// other is being made.
func (p *Plugin) canVideo() bool {
	return p.agent.Status().State == agent.Ready && !p.making.Running()
}

// agentDown reports that the backend is not connected, so a video cannot
// start.
func (p *Plugin) agentDown() bool { return p.agent.Status().State == agent.Down }

// canOpenPage reports that the full-window editor can show.
func (p *Plugin) canOpenPage() bool { return p.open && p.host.Page() != PageID }

// setRenderOptions keeps the render's format and quality.
func (p *Plugin) setRenderOptions(opts renderOptions) {
	p.renderOpts = opts
	p.ctx.Settings().Set(renderKey, opts)
}

// openPage shows the editor in the window, with no chat beside it.
func (p *Plugin) openPage() {
	if p.open {
		p.host.ShowPage(PageID)
	}
}

// newVideo opens a new chat in the video editor, in the open conversation's
// folder, or the folder whose tabs are on show, or a folder the user picks.
func (p *Plugin) newVideo() {
	folder := p.chat.Current().Directory
	if folder == "" {
		if t, ok := p.tabs(); ok {
			folder = t.Space()
		}
	}
	if folder == "" {
		p.ws.ChooseFolder(func(path string) {
			if path != "" {
				p.newVideoIn(path)
			}
		})
		return
	}
	p.newVideoIn(folder)
}

// newVideoIn makes the project's folder, videos/video-N, the first N free,
// and opens a new chat beside its first, empty composition.
func (p *Plugin) newVideoIn(folder string) {
	kernel.Go(p.ctx, &p.making, func(ctx context.Context) (string, error) {
		base := filepath.Join(folder, videosDir)
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		for n := 1; n < 10000; n++ {
			dir := filepath.Join(base, "video-"+strconv.Itoa(n))
			err := os.Mkdir(dir, 0o755)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				return "", err
			}
			if _, err := newProject(dir, "Untitled video"); err != nil {
				return "", err
			}
			return dir, nil
		}
		return "", errors.New("the folder has too many videos")
	}, func(dir string, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The video did not start", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.opening = dir
		p.chat.NewChat(folder)
		p.opening = ""
	})
}

// tabs is the tabs service, when the build installs it.
func (p *Plugin) tabs() (tabs.Service, bool) { return kernel.Use(p.ctx, tabs.ServiceKey) }

// isVideoTab reports that the tab shows a video: a conversation that is one,
// or the new video on show.
func (p *Plugin) isVideoTab(t tabs.Tab) bool {
	switch t.Kind {
	case tabs.Conversation:
		return p.videoIndex(t.Session) >= 0
	case tabs.Draft:
		if !p.draft {
			return false
		}
		s, ok := p.tabs()
		if !ok {
			return false
		}
		active, ok := s.Active()
		return ok && active.ID == t.ID
	case tabs.Page:
	}
	return false
}
