package twin

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func assertEncode(t *testing.T, incomingString string, expectedEvent Event, expectedRemainder string) {
	actualEvent, actualRemainder := consumeEncodedEvent(incomingString)

	message := strings.Replace(incomingString, "\x1b", "ESC", -1)
	message = strings.Replace(message, "\r", "RET", -1)

	assert.Assert(t, actualEvent != nil,
		"Input: %s Result: %#v Expected: %#v", message, "nil", expectedEvent)
	assert.Equal(t, *actualEvent, expectedEvent,
		"Input: %s Result: %#v Expected: %#v", message, *actualEvent, expectedEvent)
	assert.Equal(t, actualRemainder, expectedRemainder, message)
}

func TestConsumeEncodedEvent(t *testing.T) {
	assertEncode(t, "a", EventRune{rune: 'a'}, "")
	assertEncode(t, "\r", EventKeyCode{keyCode: KeyEnter}, "")
	assertEncode(t, "\x1b", EventKeyCode{keyCode: KeyEscape}, "")

	// Implicitly test having a remaining rune at the end
	assertEncode(t, "\x1b[Ax", EventKeyCode{keyCode: KeyUp}, "x")

	assertEncode(t, "\x1b[<64;127;41M", EventMouse{buttons: MouseWheelUp}, "")
	assertEncode(t, "\x1b[<65;127;41M", EventMouse{buttons: MouseWheelDown}, "")

	// This happens when users paste.
	//
	// Ref: https://github.com/walles/moar/issues/73
	assertEncode(t, "1234", EventRune{rune: '1'}, "234")
}

func TestConsumeEncodedEventWithUnsupportedEscapeCode(t *testing.T) {
	event, remainder := consumeEncodedEvent("\x1bXXXXX")
	assert.Assert(t, event == nil)
	assert.Equal(t, remainder, "")
}

func TestConsumeEncodedEventWithNoInput(t *testing.T) {
	event, remainder := consumeEncodedEvent("")
	assert.Assert(t, event == nil)
	assert.Equal(t, remainder, "")
}

func TestRenderLine(t *testing.T) {
	row := []Cell{
		{
			Rune:  '<',
			Style: StyleDefault.WithAttr(AttrReverse),
		},
		{
			Rune:  'f',
			Style: StyleDefault.WithAttr(AttrDim),
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 2)
	reset := "[m"
	reversed := "[7m"
	notReversed := "[27m"
	dim := "[2m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+reversed+"<"+dim+notReversed+"f"+reset+clearToEol, "", "ESC"))
}

func TestRenderLineEmpty(t *testing.T) {
	row := []Cell{}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 0)

	// All lines are expected to stand on their own, so we always need to clear
	// to EOL whether or not the line is empty.
	assert.Equal(t, rendered, "\x1b[m\x1b[K")
}

func TestRenderLineLastReversed(t *testing.T) {
	row := []Cell{
		{
			Rune:  '<',
			Style: StyleDefault.WithAttr(AttrReverse),
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)
	reset := "[m"
	reversed := "[7m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+reversed+"<"+reset+clearToEol, "", "ESC"))
}

func TestRenderLineLastNonSpace(t *testing.T) {
	row := []Cell{
		{
			Rune:  'X',
			Style: StyleDefault,
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)
	reset := "[m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+"X"+clearToEol, "", "ESC"))
}

func TestRenderLineLastReversedPlusTrailingSpace(t *testing.T) {
	row := []Cell{
		{
			Rune:  '<',
			Style: StyleDefault.WithAttr(AttrReverse),
		},
		{
			Rune:  ' ',
			Style: StyleDefault,
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)
	reset := "[m"
	reversed := "[7m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+reversed+"<"+reset+clearToEol, "", "ESC"))
}

func TestRenderLineOnlyTrailingSpaces(t *testing.T) {
	row := []Cell{
		{
			Rune:  ' ',
			Style: StyleDefault,
		},
		{
			Rune:  ' ',
			Style: StyleDefault,
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 0)

	// All lines are expected to stand on their own, so we always need to clear
	// to EOL whether or not the line is empty.
	assert.Equal(t, rendered, "\x1b[m\x1b[K")
}

func TestRenderLineLastReversedSpaces(t *testing.T) {
	row := []Cell{
		{
			Rune:  ' ',
			Style: StyleDefault.WithAttr(AttrReverse),
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)
	reset := "[m"
	reversed := "[7m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+reversed+" "+reset+clearToEol, "", "ESC"))
}

func TestRenderLineNonPrintable(t *testing.T) {
	row := []Cell{
		{
			Rune: '',
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)
	reset := "[m"
	white := "[37m"
	redBg := "[41m"
	bold := "[1m"
	clearToEol := "[K"
	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		strings.ReplaceAll(reset+white+redBg+bold+"?"+reset+clearToEol, "", "ESC"))
}

func TestRenderHyperlinkAtEndOfLine(t *testing.T) {
	url := "https://example.com/"
	row := []Cell{
		{
			Rune:  '*',
			Style: StyleDefault.WithHyperlink(&url),
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 1)

	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		`ESC[mESC]8;;`+url+`ESC\*ESC]8;;ESC\ESC[K`)
}

func TestMultiCharHyperlink(t *testing.T) {
	url := "https://example.com/"
	row := []Cell{
		{
			Rune:  '-',
			Style: StyleDefault.WithHyperlink(&url),
		},
		{
			Rune:  'X',
			Style: StyleDefault.WithHyperlink(&url),
		},
		{
			Rune:  '-',
			Style: StyleDefault.WithHyperlink(&url),
		},
	}

	rendered, count := renderLine(row)
	assert.Equal(t, count, 3)

	assert.Equal(t,
		strings.ReplaceAll(rendered, "", "ESC"),
		`ESC[mESC]8;;`+url+`ESC\-X-ESC]8;;ESC\ESC[K`)
}
