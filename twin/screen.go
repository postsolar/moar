// Package twin provides Terminal Window interaction
package twin

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"
	"golang.org/x/term"
)

type MouseMode int

const (
	MouseModeAuto MouseMode = iota

	// Don't capture mouse events. This makes marking with the mouse work. On
	// some terminals mouse scrolling will work using arrow keys emulation, and
	// on some not.
	MouseModeMark

	// Capture mouse events. This makes mouse scrolling work. Special gymnastics
	// will be required for marking with the mouse to copy text.
	MouseModeScroll
)

type Screen interface {
	// Close() restores terminal to normal state, must be called after you are
	// done with your screen
	Close()

	Clear()

	SetCell(column int, row int, cell Cell)

	// Render our contents into the terminal window
	Show()

	// Can be called after Close()ing the screen to fake retaining its output.
	// Plain Show() is what you'd call during normal operation.
	ShowNLines(lineCountToShow int)

	// Returns screen width and height.
	//
	// NOTE: Never cache this response! On window resizes you'll get an
	// EventResize on the Screen.Events channel, and this method will start
	// returning the new size instead.
	Size() (width int, height int)

	// ShowCursorAt() moves the cursor to the given screen position and makes
	// sure it is visible.
	//
	// If the position is outside of the screen, the cursor will be hidden.
	ShowCursorAt(column int, row int)

	// This channel is what your main loop should be checking.
	Events() chan Event
}

type UnixScreen struct {
	widthAccessFromSizeOnly  int // Access from Size() method only
	heightAccessFromSizeOnly int // Access from Size() method only
	cells                    [][]Cell

	// Note that the type here doesn't matter, we only want to know whether or
	// not this channel has been signalled
	sigwinch chan int

	events chan Event

	ttyIn            *os.File
	oldTerminalState *term.State //nolint Not used on Windows
	oldTtyInMode     uint32      //nolint Windows only

	ttyOut        *os.File
	oldTtyOutMode uint32 //nolint Windows only
}

// Example event: "\x1b[<65;127;41M"
//
// Where:
//
// * "\x1b[<" says this is a mouse event
//
// * "65" says this is Wheel Up. "64" would be Wheel Down.
//
// * "127" is the column number on screen, "1" is the first column.
//
// * "41" is the row number on screen, "1" is the first row.
//
// * "M" marks the end of the mouse event.
var MOUSE_EVENT_REGEX = regexp.MustCompile("^\x1b\\[<([0-9]+);([0-9]+);([0-9]+)M")

func NewScreen() (Screen, error) {
	return NewScreenWithMouseMode(MouseModeAuto)
}

// NewScreen() requires Close() to be called after you are done with your new
// screen, most likely somewhere in your shutdown code.
func NewScreenWithMouseMode(mouseMode MouseMode) (Screen, error) {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, fmt.Errorf("stdout (fd=%d) must be a terminal for paging to work", os.Stdout.Fd())
	}

	screen := UnixScreen{}

	// The number "80" here is from manual testing on my MacBook:
	//
	// First, start "./moar.sh sample-files/large-git-log-patch.txt".
	//
	// Then do a two finger flick initiating a momentum based scroll-up.
	//
	// Now, if you get "Events buffer full" warnings, the buffer is too small.
	//
	// By this definition, 40 was too small, and 80 was OK.
	//
	// Bumped to 160 because of: https://github.com/walles/moar/issues/164
	screen.events = make(chan Event, 160)

	screen.setupSigwinchNotification()
	err := screen.setupTtyInTtyOut()
	if err != nil {
		return nil, fmt.Errorf("problem setting up TTY: %w", err)
	}
	screen.setAlternateScreenMode(true)

	if mouseMode == MouseModeAuto {
		screen.enableMouseTracking(!terminalHasArrowKeysEmulation())
	} else if mouseMode == MouseModeMark {
		screen.enableMouseTracking(false)
	} else if mouseMode == MouseModeScroll {
		screen.enableMouseTracking(true)
	} else {
		panic(fmt.Errorf("unknown mouse mode: %d", mouseMode))
	}

	screen.hideCursor(true)

	go screen.mainLoop()

	return &screen, nil
}

// Close() restores terminal to normal state, must be called after you are done
// with the screen returned by NewScreen()
func (screen *UnixScreen) Close() {
	screen.hideCursor(false)
	screen.enableMouseTracking(false)
	screen.setAlternateScreenMode(false)

	err := screen.restoreTtyInTtyOut()
	if err != nil {
		// Debug logging because this is expected to fail in some cases:
		// * https://github.com/walles/moar/issues/145
		// * https://github.com/walles/moar/issues/149
		// * https://github.com/walles/moar/issues/150
		log.Debug("Problem restoring TTY state: ", err)
	}
}

func (screen *UnixScreen) Events() chan Event {
	return screen.events
}

// Write string to ttyOut, panic on failure, return number of bytes written.
func (screen *UnixScreen) write(string string) int {
	bytesWritten, err := screen.ttyOut.Write([]byte(string))
	if err != nil {
		panic(err)
	}
	return bytesWritten
}

func (screen *UnixScreen) setAlternateScreenMode(enable bool) {
	// Ref: https://stackoverflow.com/a/11024208/473672
	if enable {
		screen.write("\x1b[?1049h")
	} else {
		screen.write("\x1b[?1049l")
	}
}

func (screen *UnixScreen) hideCursor(hide bool) {
	// Ref: https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
	if hide {
		screen.write("\x1b[?25l")
	} else {
		screen.write("\x1b[?25h")
	}
}

// Some terminals convert mouse events to key events making scrolling better
// without our built-in mouse support, and some do not.
//
// For those that do, we're better off without mouse tracking.
//
// To test your terminal, run with `moar --mousemode=mark` and see if mouse
// scrolling still works (both down and then back up to the top). If it does,
// add another check to this function!
//
// See also: https://github.com/walles/moar/issues/53
func terminalHasArrowKeysEmulation() bool {
	// Untested:
	// * The Windows terminal

	// Better off with mouse tracking:
	// * iTerm2 (macOS)
	// * Terminal.app (macOS)

	// Hyper, tested on macOS, December 14th 2023
	if os.Getenv("TERM_PROGRAM") == "Hyper" {
		return true
	}

	// Kitty, tested on macOS, December 14th 2023
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}

	// Alacritty, tested on macOS, December 14th 2023
	if os.Getenv("ALACRITTY_WINDOW_ID") != "" {
		return true
	}

	// Warp, tested on macOS, December 14th 2023
	if os.Getenv("TERM_PROGRAM") == "WarpTerminal" {
		return true
	}

	// GNOME Terminal, tested on Ubuntu 22.04, December 16th 2023
	if os.Getenv("GNOME_TERMINAL_SCREEN") != "" {
		return true
	}

	// Tilix, tested on Ubuntu 22.04, December 16th 2023
	if os.Getenv("TILIX_ID") != "" {
		return true
	}

	// Konsole, tested on Ubuntu 22.04, December 16th 2023
	if os.Getenv("KONSOLE_VERSION") != "" {
		return true
	}

	// Terminator, tested on Ubuntu 22.04, December 16th 2023
	if os.Getenv("TERMINATOR_UUID") != "" {
		return true
	}

	// Foot, tested on Ubuntu 22.04, December 16th 2023
	if os.Getenv("TERM") == "foot" || strings.HasPrefix(os.Getenv("TERM"), "foot-") {
		// Note that this test isn't very good, somebody could be running Foot
		// with some other TERM setting. Other suggestions welcome.
		return true
	}

	return false
}

func (screen *UnixScreen) enableMouseTracking(enable bool) {
	if enable {
		screen.write("\x1b[?1006;1000h")
	} else {
		screen.write("\x1b[?1006;1000l")
	}
}

// ShowCursorAt() moves the cursor to the given screen position and makes sure
// it is visible.
//
// If the position is outside of the screen, the cursor will be hidden.
func (screen *UnixScreen) ShowCursorAt(column int, row int) {
	if column < 0 {
		screen.hideCursor(true)
		return
	}
	if row < 0 {
		screen.hideCursor(true)
		return
	}

	width, height := screen.Size()
	if column >= width {
		screen.hideCursor(true)
		return
	}
	if row >= height {
		screen.hideCursor(true)
		return
	}

	// https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
	screen.write(fmt.Sprintf("\x1b[%d;%dH", row, column))
	screen.hideCursor(false)
}

func (screen *UnixScreen) mainLoop() {
	// "1400" comes from me trying fling scroll operations on my MacBook
	// trackpad and looking at the high watermark (logged below).
	//
	// The highest I saw when I tried this was 700 something. 1400 is twice
	// that, so 1400 should be good.
	buffer := make([]byte, 1400)

	maxBytesRead := 0
	for {
		count, err := screen.ttyIn.Read(buffer)
		if err != nil {
			// Ref:
			// * https://github.com/walles/moar/issues/145
			// * https://github.com/walles/moar/issues/149
			// * https://github.com/walles/moar/issues/150
			log.Debug("ttyin read error, twin giving up: ", err)

			var event Event = EventExit{}
			screen.events <- event
			return
		}

		if count > maxBytesRead {
			maxBytesRead = count
			log.Trace("ttyin high watermark bumped to ", maxBytesRead, " bytes")
		}

		encodedKeyCodeSequences := string(buffer[0:count])
		if !utf8.ValidString(encodedKeyCodeSequences) {
			log.Warn("Got invalid UTF-8 sequence on ttyin: ", encodedKeyCodeSequences)
			continue
		}

		for len(encodedKeyCodeSequences) > 0 {
			var event *Event
			event, encodedKeyCodeSequences = consumeEncodedEvent(encodedKeyCodeSequences)

			if event == nil {
				// No event, go wait for more
				break
			}

			// Post the event
			select {
			case screen.events <- *event:
				// Yay
			default:
				// If this happens, consider increasing the channel size in
				// NewScreen()
				log.Debugf("Events buffer (size %d) full, events are being dropped", cap(screen.events))
			}
		}
	}
}

// Turn ESC into <0x1b> and other low ASCII characters into <0xXX> for logging
// purposes.
func humanizeLowAscii(withLowAsciis string) string {
	humanized := ""
	for _, char := range withLowAsciis {
		if char < ' ' {
			humanized += fmt.Sprintf("<0x%2x>", char)
			continue
		}
		humanized += string(char)
	}
	return humanized
}

// Consume initial key code from the sequence of encoded keycodes.
//
// Returns a (possibly nil) event that should be posted, and the remainder of
// the encoded events sequence.
func consumeEncodedEvent(encodedEventSequences string) (*Event, string) {
	for singleKeyCodeSequence, keyCode := range escapeSequenceToKeyCode {
		if !strings.HasPrefix(encodedEventSequences, singleKeyCodeSequence) {
			continue
		}

		// Encoded key code sequence found, report it!
		var event Event = EventKeyCode{keyCode}
		return &event, strings.TrimPrefix(encodedEventSequences, singleKeyCodeSequence)
	}

	mouseMatch := MOUSE_EVENT_REGEX.FindStringSubmatch(encodedEventSequences)
	if mouseMatch != nil {
		if mouseMatch[1] == "64" {
			var event Event = EventMouse{buttons: MouseWheelUp}
			return &event, strings.TrimPrefix(encodedEventSequences, mouseMatch[0])
		}
		if mouseMatch[1] == "65" {
			var event Event = EventMouse{buttons: MouseWheelDown}
			return &event, strings.TrimPrefix(encodedEventSequences, mouseMatch[0])
		}

		log.Debug(
			"Unhandled multi character mouse escape sequence(s): {",
			humanizeLowAscii(encodedEventSequences),
			"}")
		return nil, ""
	}

	// No escape sequence prefix matched
	runes := []rune(encodedEventSequences)
	if len(runes) == 0 {
		return nil, ""
	}

	if runes[0] == '\x1b' {
		if len(runes) != 1 {
			// This means one or more sequences should be added to
			// escapeSequenceToKeyCode in keys.go.
			log.Debug(
				"Unhandled multi character terminal escape sequence(s): {",
				humanizeLowAscii(encodedEventSequences),
				"}")

			// Mark everything as consumed since we don't know how to proceed otherwise.
			return nil, ""
		}

		var event Event = EventKeyCode{KeyEscape}
		return &event, string(runes[1:])
	}

	if runes[0] == '\r' {
		var event Event = EventKeyCode{KeyEnter}
		return &event, string(runes[1:])
	}

	// Report the single rune
	var event Event = EventRune{rune: runes[0]}
	return &event, string(runes[1:])
}

// Returns screen width and height.
//
// NOTE: Never cache this response! On window resizes you'll get an EventResize
// on the Screen.Events channel, and this method will start returning the new
// size instead.
func (screen *UnixScreen) Size() (width int, height int) {
	select {
	case <-screen.sigwinch:
		// Resize logic needed, see below
	default:
		// No resize, go with the existing values
		return screen.widthAccessFromSizeOnly, screen.heightAccessFromSizeOnly
	}

	// Window was resized
	width, height, err := term.GetSize(int(screen.ttyOut.Fd()))
	if err != nil {
		panic(err)
	}

	if screen.widthAccessFromSizeOnly == width && screen.heightAccessFromSizeOnly == height {
		// Not sure when this would happen, but if it does this wasn't really a
		// resize, and we don't need to treat it as such.
		return screen.widthAccessFromSizeOnly, screen.heightAccessFromSizeOnly
	}

	newCells := make([][]Cell, height)
	for rowNumber := 0; rowNumber < height; rowNumber++ {
		newCells[rowNumber] = make([]Cell, width)
	}

	// FIXME: Copy any existing contents over to the new, resized screen array
	// FIXME: Fill any non-initialized cells with whitespace

	screen.widthAccessFromSizeOnly = width
	screen.heightAccessFromSizeOnly = height
	screen.cells = newCells

	return screen.widthAccessFromSizeOnly, screen.heightAccessFromSizeOnly
}

func (screen *UnixScreen) SetCell(column int, row int, cell Cell) {
	if column < 0 {
		return
	}
	if row < 0 {
		return
	}

	width, height := screen.Size()
	if column >= width {
		return
	}
	if row >= height {
		return
	}
	screen.cells[row][column] = cell
}

func (screen *UnixScreen) Clear() {
	empty := NewCell(' ', StyleDefault)

	width, height := screen.Size()
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			screen.cells[row][column] = empty
		}
	}
}

// Returns the rendered line, plus how many information carrying cells went into
// it
func renderLine(row []Cell) (string, int) {
	// Strip trailing whitespace
	lastSignificantCellIndex := len(row) - 1
	for ; lastSignificantCellIndex >= 0; lastSignificantCellIndex-- {
		lastCell := row[lastSignificantCellIndex]
		if lastCell.Rune != ' ' || lastCell.Style != StyleDefault {
			break
		}
	}
	row = row[0 : lastSignificantCellIndex+1]

	var builder strings.Builder

	// Set initial line style to normal
	builder.WriteString("\x1b[m")
	lastStyle := StyleDefault

	for column := 0; column < len(row); column++ {
		cell := row[column]

		style := cell.Style
		runeToWrite := cell.Rune
		if !Printable(runeToWrite) {
			// Highlight unprintable runes
			style = Style{
				fg:    NewColor16(7), // White
				bg:    NewColor16(1), // Red
				attrs: AttrBold,
			}
			runeToWrite = '?'
		}

		if style != lastStyle {
			builder.WriteString(style.RenderUpdateFrom(lastStyle))
			lastStyle = style
		}

		builder.WriteRune(runeToWrite)
	}

	// Clear to end of line
	// https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
	builder.WriteString(StyleDefault.RenderUpdateFrom(lastStyle))
	builder.WriteString("\x1b[K")

	return builder.String(), len(row)
}

func (screen *UnixScreen) Show() {
	_, height := screen.Size()
	screen.showNLines(height, true)
}

func (screen *UnixScreen) ShowNLines(height int) {
	screen.showNLines(height, false)
}

func (screen *UnixScreen) showNLines(height int, clearFirst bool) {
	var builder strings.Builder

	if clearFirst {
		// Start in the top left corner:
		// https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
		builder.WriteString("\x1b[1;1H")
	}

	for row := 0; row < height; row++ {
		rendered, lineLength := renderLine(screen.cells[row])
		builder.WriteString(rendered)

		wasLastLine := row == (height - 1)

		// NOTE: This <= should *really* be <= and nothing else. Otherwise, if
		// one line precisely as long as the terminal window goes before one
		// empty line, the empty line will never be rendered.
		//
		// Can be demonstrated using "moar m/pager.go", scroll right once to
		// make the line numbers go away, then make the window narrower until
		// some line before an empty line is just as wide as the window.
		//
		// With the wrong comparison here, then the empty line just disappears.
		if lineLength <= len(screen.cells[row]) && !wasLastLine {
			builder.WriteString("\r\n")
		}
	}

	// Write out what we have
	screen.write(builder.String())
}
