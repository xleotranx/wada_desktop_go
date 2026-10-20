package main

import "github.com/egoist/mygo/ui"

// Conversation text choices. They scale the transcript and Activity view,
// not the navigation chrome or the composer, which keeps the window's
// controls a steady size while the words being read grow.
const (
	chatTextSmall   = "small"
	chatTextDefault = "default"
	chatTextLarge   = "large"
	chatTextHuge    = "huge"
)

type chatTextChoice struct {
	value, label string
	scale        float32
}

var chatTextChoices = []chatTextChoice{
	{chatTextSmall, "Small", .9},
	{chatTextDefault, "Default", 1},
	{chatTextLarge, "Large", 1.15},
	{chatTextHuge, "Largest", 1.3},
}

// normalizeChatText maps a stored value onto a size this build knows.
func normalizeChatText(value string) string {
	for _, choice := range chatTextChoices {
		if choice.value == value {
			return value
		}
	}
	return chatTextDefault
}

// chatTextScale is the multiplier the conversation surfaces apply.
func chatTextScale(value string) float32 {
	value = normalizeChatText(value)
	for _, choice := range chatTextChoices {
		if choice.value == value {
			return choice.scale
		}
	}
	return 1
}

// chatFont sizes one transcript measurement at the chosen text size.
func (a *app) chatFont(base float32) float32 {
	return base * chatTextScale(a.preferences.chatText)
}

// setChatText changes the conversation size and saves the choice.
func (a *app) setChatText(value string) {
	value = normalizeChatText(value)
	if a.preferences.chatText == value {
		return
	}
	a.preferences.chatText = value
	a.saveState()
}

// bumpChatText steps the size up or down, never past either end, and says
// where it landed in a toast.
func (a *app) bumpChatText(delta int) {
	current := normalizeChatText(a.preferences.chatText)
	index := 0
	for i, choice := range chatTextChoices {
		if choice.value == current {
			index = i
			break
		}
	}
	next := min(max(index+delta, 0), len(chatTextChoices)-1)
	value := chatTextChoices[next]
	a.setChatText(value.value)
	a.notify(ui.Toast{Title: "Conversation text: " + value.label})
}
