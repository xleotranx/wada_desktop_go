package desktop

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	onechat "github.com/oneo-me/onevra/source/components/chat"
)

// queueTurn remembers a prompt the runner accepted while another turn
// was active. The runner processes one job at a time, so the prompt waits
// its turn; the list explains the pause in the composer.
func (a *app) queueTurn(id, text string) {
	if id == "" {
		return
	}
	scope := a.scope()
	if scope.queued == nil {
		scope.queued = map[string][]string{}
	}
	scope.queued[id] = append(scope.queued[id], text)
}

// queuedTurns counts the prompts waiting behind a chat's active turn.
func (a *app) queuedTurns(id string) int {
	return len(a.scope().queued[id])
}

// queuedPrompts lists a chat's waiting prompts, oldest first.
func (a *app) queuedPrompts(id string) []string {
	return slices.Clone(a.scope().queued[id])
}

// queuedLabel words how many messages wait for one chat's turn.
func queuedLabel(n int) string {
	if n == 1 {
		return "1 message queued"
	}
	return fmt.Sprintf("%d messages queued", n)
}

// startQueuedTurn releases one waiting prompt when its turn starts, from
// whichever chat the stream reports. The panel closes once nothing waits.
func (a *app) startQueuedTurn(id string) {
	scope := a.scope()
	if len(scope.queued[id]) == 0 {
		return
	}
	scope.queued[id] = scope.queued[id][1:]
	if len(scope.queued[id]) == 0 {
		delete(scope.queued, id)
		a.layout.queueOpen = false
	}
}

// queuedMessages is the composer's note that prompts wait behind the
// running turn: the pill and the list of what will run, in order.
func (a *app) queuedMessages(c *ui.Context, p palette) {
	sid := string(a.conversation.target.session)
	n := a.queuedTurns(sid)
	if n <= 0 {
		a.layout.queueOpen = false
		return
	}
	ui.Row(c).MaxWidth(onechat.ContentWidth).Margin(0, ui.Auto).FillWidth().
		Padding(0, 0, 4, 0).Children(func() {
		pill := ui.ButtonBase(c).Label(queuedLabel(n)).Height(24).Radius(999).
			Padding(0, 10).Gap(5).Background(p.DetailBg).
			Tooltip("Messages waiting for this turn").Cursor(ui.CursorPointer)
		if pill.Hovered() {
			pill.Background(p.Hover)
		}
		pill.OnClick(func() { a.layout.queueOpen = !a.layout.queueOpen })
		pill.Children(func() {
			ui.Icon(c, icHistory).FontSize(12).TextColor(p.Muted)
			ui.Text(c, queuedLabel(n)).FontSize(11.5).TextColor(p.Muted)
			ui.Text(c, "· runs after this turn").FontSize(11.5).TextColor(p.Faint)
		})
		components.Popover(c, pill, &a.layout.queueOpen, func(panel ui.Element) {
			panel.Width(340).MaxHeight(380).Children(func() {
				ui.Text(c, "Waiting behind this turn").FontSize(13).FontWeight(600).
					TextColor(p.Text).SingleLine()
				ui.Scroll(c).FillWidth().MaxHeight(250).Gap(4).Children(func() {
					for i, text := range a.queuedPrompts(sid) {
						c.Key(fmt.Sprintf("queued:%d", i))
						ui.Column(c).Label("Queued message: "+text).FillWidth().
							Radius(8).Padding(6, 9).Gap(2).
							Background(p.DetailBg).Border(1, p.Hairline).Children(func() {
							ui.Text(c, fmt.Sprintf("%d", i+1)).Font("monospace").FontSize(10).
								TextColor(p.Faint)
							ui.Text(c, text).FontSize(12.5).TextColor(p.Text).LineHeight(1.4)
						})
					}
				})
				ui.Text(c, "They run in order when this turn ends.").FontSize(11.5).
					LineHeight(1.4).TextColor(p.Muted)
			})
		})
	})
}
