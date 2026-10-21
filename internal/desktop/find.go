package desktop

import (
	"strings"

	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// openChatFind opens the transcript's find bar on the conversation on
// show. Settings and a draft have no transcript to search, so the key that
// opens it leaves them alone.
func (a *app) openChatFind() {
	if a.layout.settingsOpen || a.conversation.target.isDraft() {
		return
	}
	a.layout.findOpen = true
	a.layout.findMatch = 0
	a.layout.findShownMatch = -1
}

// chatFindRows counts the transcript's matches and remembers which row
// holds each: one entry per occurrence, in reading order, so the find bar
// can bring the row it names into view. A query matches case-insensitively
// wherever a row shows text: the line itself and the paths an approval
// lists.
func (a *app) chatFindRows() []int {
	query := strings.ToLower(a.layout.findQuery)
	if query == "" {
		return nil
	}
	var rows []int
	for i := range a.conversation.transcript.Len() {
		block := a.conversation.transcript.Row(i)
		n := strings.Count(strings.ToLower(findRowText(block)), query)
		for range n {
			rows = append(rows, i)
		}
	}
	return rows
}

// chatFindBar floats MyGo's find bar over the transcript. The app owns the
// counting, so this bar's next and previous moves land on the row that
// holds the match; another frame scrolls it into the middle of the feed.
// The match and query last shown stay beside the bar, so a reader who
// scrolls away is not dragged back on every frame.
func (a *app) chatFindBar(c *ui.Context, p palette) {
	rows := a.chatFindRows()
	if a.layout.findQuery != a.layout.findQuerySeen {
		a.layout.findQuerySeen = a.layout.findQuery
		a.layout.findMatch = 0
		a.layout.findShownMatch = -1
	}
	count := len(rows)
	if count == 0 {
		a.layout.findShownMatch = -1
	} else {
		a.layout.findMatch = min(max(a.layout.findMatch, 0), count-1)
	}
	ui.Box(c).Absolute().Top(10).Left(14).Right(14).Radius(10).Clip().
		Background(c.Theme().Background).Border(1, p.Hairline).Children(func() {
		ui.FindBar(c, &a.layout.findOpen, &a.layout.findQuery, count, &a.layout.findMatch).FillWidth()
	})
	if !a.layout.findOpen {
		a.layout.findShownMatch = -1
		a.layout.findShownQuery = ""
		return
	}
	if count > 0 && (a.layout.findMatch != a.layout.findShownMatch ||
		a.layout.findQuery != a.layout.findShownQuery) {
		a.layout.feed.ScrollTo(rows[a.layout.findMatch], ui.Center)
		a.layout.findShownMatch = a.layout.findMatch
		a.layout.findShownQuery = a.layout.findQuery
	}
}

// findRowText is the text find looks through for one block: what the row
// shows on screen, including the paths an approval lists but not the
// detail an activity keeps folded away.
func findRowText(block chat.Block) string {
	var text strings.Builder
	text.WriteString(block.Text)
	if block.Request != nil {
		text.WriteByte('\n')
		text.WriteString(block.Request.Action)
		for _, line := range block.Request.Resources {
			text.WriteByte('\n')
			text.WriteString(line)
		}
		for _, line := range block.Request.Save {
			text.WriteByte('\n')
			text.WriteString(line)
		}
	}
	return text.String()
}
