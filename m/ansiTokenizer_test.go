package m

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	log "github.com/sirupsen/logrus"

	"github.com/walles/moar/twin"
	"gotest.tools/v3/assert"
)

// Convert a cells array to a plain string
func cellsToPlainString(cells []twin.Cell) string {
	returnMe := ""
	for _, cell := range cells {
		returnMe += string(cell.Rune)
	}

	return returnMe
}

// Verify that we can tokenize all lines in ../sample-files/*
// without logging any errors
func TestTokenize(t *testing.T) {
	for _, fileName := range getTestFiles() {
		t.Run(fileName, func(t *testing.T) {
			file, err := os.Open(fileName)
			if err != nil {
				t.Errorf("Error opening file <%s>: %s", fileName, err.Error())
				return
			}
			defer func() {
				if err := file.Close(); err != nil {
					panic(err)
				}
			}()

			myReader := NewReaderFromStream(fileName, file)
			for !myReader.done.Load() {
			}

			for lineNumber := 1; lineNumber <= myReader.GetLineCount(); lineNumber++ {
				line := myReader.GetLine(lineNumber)
				lineNumber++

				var loglines strings.Builder
				log.SetOutput(&loglines)

				tokens := cellsFromString(line.raw, &lineNumber).Cells
				plainString := withoutFormatting(line.raw, &lineNumber)
				if len(tokens) != utf8.RuneCountInString(plainString) {
					t.Errorf("%s:%d: len(tokens)=%d, len(plainString)=%d for: <%s>",
						fileName, lineNumber,
						len(tokens), utf8.RuneCountInString(plainString), line.raw)
					continue
				}

				// Tokens and plain have the same lengths, compare contents
				plainStringChars := []rune(plainString)
				for index, plainChar := range plainStringChars {
					cellChar := tokens[index]
					if cellChar.Rune == plainChar {
						continue
					}

					if cellChar.Rune == '•' && plainChar == 'o' {
						// Pretty bullets on man pages
						continue
					}

					// Chars mismatch!
					plainStringFromCells := cellsToPlainString(tokens)
					positionMarker := strings.Repeat(" ", index) + "^"
					cellCharString := string(cellChar.Rune)
					if !twin.Printable(cellChar.Rune) {
						cellCharString = fmt.Sprint(int(cellChar.Rune))
					}
					plainCharString := string(plainChar)
					if !twin.Printable(plainChar) {
						plainCharString = fmt.Sprint(int(plainChar))
					}
					t.Errorf("%s:%d, 0-based column %d: cell char <%s> != plain char <%s>:\nPlain: %s\nCells: %s\n       %s",
						fileName, lineNumber, index,
						cellCharString, plainCharString,
						plainString,
						plainStringFromCells,
						positionMarker,
					)
					break
				}

				if len(loglines.String()) != 0 {
					t.Errorf("%s: %s", fileName, loglines.String())
					continue
				}
			}
		})
	}
}

func TestUnderline(t *testing.T) {
	tokens := cellsFromString("a\x1b[4mb\x1b[24mc", nil).Cells
	assert.Equal(t, len(tokens), 3)
	assert.Equal(t, tokens[0], twin.Cell{Rune: 'a', Style: twin.StyleDefault})
	assert.Equal(t, tokens[1], twin.Cell{Rune: 'b', Style: twin.StyleDefault.WithAttr(twin.AttrUnderline)})
	assert.Equal(t, tokens[2], twin.Cell{Rune: 'c', Style: twin.StyleDefault})
}

func TestManPages(t *testing.T) {
	// Bold
	tokens := cellsFromString("ab\bbc", nil).Cells
	assert.Equal(t, len(tokens), 3)
	assert.Equal(t, tokens[0], twin.Cell{Rune: 'a', Style: twin.StyleDefault})
	assert.Equal(t, tokens[1], twin.Cell{Rune: 'b', Style: twin.StyleDefault.WithAttr(twin.AttrBold)})
	assert.Equal(t, tokens[2], twin.Cell{Rune: 'c', Style: twin.StyleDefault})

	// Underline
	tokens = cellsFromString("a_\bbc", nil).Cells
	assert.Equal(t, len(tokens), 3)
	assert.Equal(t, tokens[0], twin.Cell{Rune: 'a', Style: twin.StyleDefault})
	assert.Equal(t, tokens[1], twin.Cell{Rune: 'b', Style: twin.StyleDefault.WithAttr(twin.AttrUnderline)})
	assert.Equal(t, tokens[2], twin.Cell{Rune: 'c', Style: twin.StyleDefault})

	// Bullet point 1, taken from doing this on my macOS system:
	// env PAGER="hexdump -C" man printf | moar
	tokens = cellsFromString("a+\b+\bo\bob", nil).Cells
	assert.Equal(t, len(tokens), 3)
	assert.Equal(t, tokens[0], twin.Cell{Rune: 'a', Style: twin.StyleDefault})
	assert.Equal(t, tokens[1], twin.Cell{Rune: '•', Style: twin.StyleDefault})
	assert.Equal(t, tokens[2], twin.Cell{Rune: 'b', Style: twin.StyleDefault})

	// Bullet point 2, taken from doing this using the "fish" shell on my macOS system:
	// man printf | hexdump -C | moar
	tokens = cellsFromString("a+\bob", nil).Cells
	assert.Equal(t, len(tokens), 3)
	assert.Equal(t, tokens[0], twin.Cell{Rune: 'a', Style: twin.StyleDefault})
	assert.Equal(t, tokens[1], twin.Cell{Rune: '•', Style: twin.StyleDefault})
	assert.Equal(t, tokens[2], twin.Cell{Rune: 'b', Style: twin.StyleDefault})
}

func TestConsumeCompositeColorHappy(t *testing.T) {
	// 8 bit color
	// Example from: https://github.com/walles/moar/issues/14
	newIndex, color, err := consumeCompositeColor([]uint{38, 5, 74}, 0)
	assert.NilError(t, err)
	assert.Equal(t, newIndex, 3)
	assert.Equal(t, *color, twin.NewColor256(74))

	// 24 bit color
	newIndex, color, err = consumeCompositeColor([]uint{38, 2, 10, 20, 30}, 0)
	assert.NilError(t, err)
	assert.Equal(t, newIndex, 5)
	assert.Equal(t, *color, twin.NewColor24Bit(10, 20, 30))
}

func TestConsumeCompositeColorBadPrefix(t *testing.T) {
	// 8 bit color
	// Example from: https://github.com/walles/moar/issues/14
	_, color, err := consumeCompositeColor([]uint{29}, 0)
	assert.Equal(t, err.Error(), "unknown start of color sequence <29>, expected 38 (foreground) or 48 (background): <CSI 29m>")
	assert.Assert(t, color == nil)
}

func TestConsumeCompositeColorBadType(t *testing.T) {
	_, color, err := consumeCompositeColor([]uint{38, 4}, 0)
	// https://en.wikipedia.org/wiki/ANSI_escape_code#Colors
	assert.Equal(t, err.Error(), "unknown color type <4>, expected 5 (8 bit color) or 2 (24 bit color): <CSI 38;4m>")
	assert.Assert(t, color == nil)
}

func TestConsumeCompositeColorIncomplete(t *testing.T) {
	_, color, err := consumeCompositeColor([]uint{38}, 0)
	assert.Equal(t, err.Error(), "incomplete color sequence: <CSI 38m>")
	assert.Assert(t, color == nil)
}

func TestConsumeCompositeColorIncomplete8Bit(t *testing.T) {
	_, color, err := consumeCompositeColor([]uint{38, 5}, 0)
	assert.Equal(t, err.Error(), "incomplete 8 bit color sequence: <CSI 38;5m>")
	assert.Assert(t, color == nil)
}

func TestConsumeCompositeColorIncomplete24Bit(t *testing.T) {
	_, color, err := consumeCompositeColor([]uint{38, 2, 10, 20}, 0)
	assert.Equal(t, err.Error(), "incomplete 24 bit color sequence, expected N8;2;R;G;Bm: <CSI 38;2;10;20m>")
	assert.Assert(t, color == nil)
}

func TestRawUpdateStyle(t *testing.T) {
	numberColored, _, err := rawUpdateStyle(twin.StyleDefault, "33m", make([]uint, 0))
	assert.NilError(t, err)
	assert.Equal(t, numberColored, twin.StyleDefault.Foreground(twin.NewColor16(3)))
}

// Test with the recommended terminator ESC-backslash.
//
// Ref: https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda#the-escape-sequence
func TestHyperlink_escBackslash(t *testing.T) {
	url := "http://example.com"

	tokens := cellsFromString("a\x1b]8;;"+url+"\x1b\\bc\x1b]8;;\x1b\\d", nil).Cells

	assert.DeepEqual(t, tokens, []twin.Cell{
		{Rune: 'a', Style: twin.StyleDefault},
		{Rune: 'b', Style: twin.StyleDefault.WithHyperlink(&url)},
		{Rune: 'c', Style: twin.StyleDefault.WithHyperlink(&url)},
		{Rune: 'd', Style: twin.StyleDefault},
	}, cmp.AllowUnexported(twin.Style{}))
}

// Test with the not-recommended terminator BELL (ASCII 7).
//
// Ref: https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda#the-escape-sequence
func TestHyperlink_bell(t *testing.T) {
	url := "http://example.com"

	tokens := cellsFromString("a\x1b]8;;"+url+"\x07bc\x1b]8;;\x07d", nil).Cells

	assert.DeepEqual(t, tokens, []twin.Cell{
		{Rune: 'a', Style: twin.StyleDefault},
		{Rune: 'b', Style: twin.StyleDefault.WithHyperlink(&url)},
		{Rune: 'c', Style: twin.StyleDefault.WithHyperlink(&url)},
		{Rune: 'd', Style: twin.StyleDefault},
	}, cmp.AllowUnexported(twin.Style{}))
}

// Test with some other ESC sequence than ESC-backslash
func TestHyperlink_nonTerminatingEsc(t *testing.T) {
	complete := "a\x1b]8;;https://example.com\x1bbc"
	tokens := cellsFromString(complete, nil).Cells

	// This should not be treated as any link
	for i := 0; i < len(complete); i++ {
		if complete[i] == '\x1b' {
			// These get special rendering, if everything else matches that's
			// good enough.
			continue
		}
		assert.Equal(t, tokens[i], twin.Cell{Rune: rune(complete[i]), Style: twin.StyleDefault},
			"i=%d, c=%s, tokens=%v", i, string(complete[i]), tokens)
	}
}

func TestHyperlink_incomplete(t *testing.T) {
	complete := "a\x1b]8;;X\x1b\\"

	for l := len(complete) - 1; l >= 0; l-- {
		incomplete := complete[:l]
		t.Run(fmt.Sprintf("l=%d incomplete=<%s>", l, strings.ReplaceAll(incomplete, "\x1b", "ESC")), func(t *testing.T) {
			tokens := cellsFromString(incomplete, nil).Cells

			for i := 0; i < l; i++ {
				if complete[i] == '\x1b' {
					// These get special rendering, if everything else matches
					// that's good enough.
					continue
				}
				assert.Equal(t, tokens[i], twin.Cell{Rune: rune(complete[i]), Style: twin.StyleDefault})
			}
		})
	}
}
