package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestModalRenderersFitResizesAndPreserveInputs(t *testing.T) {
	long := "//depot/" + strings.Repeat("特🌿élong_", 30)
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 40}, {Width: 40, Height: 12}, {Width: 20, Height: 8}} {
		for _, name := range []string{"submit", "shelve", "move", "login", "classic source", "classic target", "stream integrate", "checkout", "checkout with shelf", "accept theirs", "accept yours", "delete shelf", "switch", "help"} {
			t.Run(fmt.Sprintf("%s/%dx%d", name, size.Width, size.Height), func(t *testing.T) {
				a := New(&p4.Client{StorePassword: true}, 0, "", false)
				a.Update(size)
				var render func() string
				var inputs []*textinput.Model
				switch name {
				case "submit":
					a.modal = newSubmitModal("41", nil)
					render, inputs = a.renderModal, []*textinput.Model{&a.modal.input}
				case "shelve":
					a.shelveModal = &shelveDescModal{input: textinput.New(), files: []p4.OpenedFile{{ClientFile: "//workspace/frozen.txt"}}, noRevert: true}
					render, inputs = a.renderShelveModal, []*textinput.Model{&a.shelveModal.input}
				case "move":
					a.moveModal = &moveCLModal{input: textinput.New(), files: []p4.OpenedFile{{ClientFile: "//workspace/frozen.txt"}}}
					render, inputs = a.renderMoveModal, []*textinput.Model{&a.moveModal.input}
				case "login":
					a.authModal = newAuthModal()
					render, inputs = a.renderAuthModal, []*textinput.Model{&a.authModal.input}
				case "classic source", "classic target":
					a.integrateModal = newIntegrateModalClassic()
					if name == "classic target" {
						a.integrateModal.step = 1
						a.integrateModal.sourceInput.Blur()
						a.integrateModal.targetInput.Focus()
					}
					render, inputs = a.renderIntegrateModal, []*textinput.Model{&a.integrateModal.sourceInput, &a.integrateModal.targetInput}
				case "stream integrate":
					a.integrateModal = newIntegrateModalStream("//depot/feature", "//depot/main")
					render = a.renderIntegrateModal
				case "checkout", "checkout with shelf":
					a.checkout = &checkoutModal{cl: "41", stream: "//depot/frozen", hasFiles: name == "checkout with shelf"}
					render = a.renderCheckoutModal
				case "accept theirs", "accept yours", "delete shelf":
					a.confirm = &confirmModal{kind: confirmKindResolve, files: []string{"//workspace/frozen.txt"}, resolveFlags: []string{"-ay"}}
					if name == "accept theirs" {
						a.confirm.resolveFlags = []string{"-at"}
					} else if name == "delete shelf" {
						a.confirm.kind, a.confirm.clID = confirmKindDeleteShelf, "41"
					}
					render = a.renderConfirmModal
				case "switch":
					a.streamSwitch = &streamSwitchModal{stream: "//depot/" + long, switching: true}
					render = a.renderStreamSwitchModal
				case "help":
					a.showHelp = true
					a.helpViewport.SetContent(a.helpContent())
					render = a.renderHelpModal
				}
				type snapshot struct {
					value, prompt   string
					position, width int
					focused         bool
					echo            textinput.EchoMode
				}
				snapshots := make([]snapshot, len(inputs))
				for i, input := range inputs {
					input.Width = 50
					input.SetValue(long)
					input.SetCursor(7)
					snapshots[i] = snapshot{input.Value(), input.Prompt, input.Position(), input.Width, input.Focused(), input.EchoMode}
				}
				view := render()
				lines := strings.Split(view, "\n")
				if !utf8.ValidString(view) || len(lines) > size.Height-1 {
					t.Fatalf("modal exceeds height or contains invalid Unicode: %q", view)
				}
				for i, line := range lines {
					if lipgloss.Width(line) > size.Width-2 {
						t.Fatalf("line %d exceeds popup width: %q", i, ansi.Strip(line))
					}
				}
				if name == "classic target" && !strings.Contains(ansi.Strip(view), "Target") {
					t.Fatal("long source path hid the active target input")
				}
				if name == "login" && strings.Contains(ansi.Strip(view), "long_") {
					t.Fatal("password rendered without masking")
				}
				if frame := a.View(); len(strings.Split(frame, "\n")) > size.Height {
					t.Fatal("modal frame exceeds terminal height")
				}
				a.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
				a.View()
				for i, input := range inputs {
					got := snapshot{input.Value(), input.Prompt, input.Position(), input.Width, input.Focused(), input.EchoMode}
					if got != snapshots[i] {
						t.Fatal("rendering or resizing changed the captured input value, cursor, focus, or masking")
					}
				}
			})
		}
	}
}

func TestModalFooterHintsDescribeCurrentAction(t *testing.T) {
	for _, test := range []struct {
		name   string
		open   func(*App)
		want   string
		absent string
	}{
		{"submit", func(a *App) { a.modal = newSubmitModal("41", nil) }, "Submit: <enter>", "Remember"},
		{"move", func(a *App) { a.moveModal = &moveCLModal{} }, "Move: <enter>", "Submit:"},
		{"shelve and revert", func(a *App) { a.shelveModal = &shelveDescModal{} }, "Shelve and revert", "Submit:"},
		{"shelve only", func(a *App) { a.shelveModal = &shelveDescModal{noRevert: true} }, "Shelve: <enter>", "revert"},
		{"checkout", func(a *App) { a.checkout = &checkoutModal{} }, "Sync: <enter>", "Shelve"},
		{"shelve checkout", func(a *App) { a.checkout = &checkoutModal{hasFiles: true} }, "Shelve and sync", "Submit:"},
		{"classic source", func(a *App) { a.integrateModal = newIntegrateModalClassic() }, "Next: <enter>", "Promote:"},
		{"classic target", func(a *App) { a.integrateModal = newIntegrateModalClassic(); a.integrateModal.step = 1 }, "Integrate: <enter>", "Next:"},
		{"stream integrate", func(a *App) { a.integrateModal = newIntegrateModalStream("//d/main", "//d/parent") }, "Pull: m | Promote: c", "Execute:"},
		{"help", func(a *App) { a.showHelp = true }, "Scroll: j/k | Close: <esc>", "Submit:"},
		{"switch", func(a *App) { a.streamSwitch = &streamSwitchModal{switching: true} }, "Switching workspace", "Cancel"},
		{"login", func(a *App) { a.authModal = newAuthModal() }, "Login: <enter>", "ctrl+enter"},
		{"remember login", func(a *App) { a.authModal = newAuthModal(); a.client.StorePassword = true }, "Login and remember: <ctrl+enter>", "Submit:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.width = 120
			test.open(a)
			hint := ansi.Strip(a.renderHotkeys())
			if !strings.Contains(hint, test.want) || strings.Contains(hint, test.absent) {
				t.Fatalf("incorrect modal footer hint: %q", hint)
			}
		})
	}
}

func TestBusySubmitModalRetainsDescriptionUntilRetry(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	m := newSubmitModal("", []p4.OpenedFile{{ClientFile: "//workspace/frozen.txt", Action: p4.ActionAdd}})
	m.input.SetValue("captured description")
	a.modal = m
	a.opRunning, a.opName, a.opID = true, "Resolving", 7
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if unwrapActivityResult(cmd) != nil || a.modal != m {
		t.Fatal("busy submit discarded its description or started an operation")
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	a.Update(opEndMsg{id: 7})
	if a.modal != m || m.input.Value() != "captured description" || a.opRunning {
		t.Fatal("completion or resize changed the queued submit form")
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.opCancel != nil {
		t.Cleanup(a.opCancel)
	}
	ready, ok := unwrapActivityResult(cmd).(submitReadyMsg)
	if !ok || a.modal != nil || !a.opRunning || ready.description != "captured description" || len(ready.files) != 1 || ready.files[0].ClientFile != "//workspace/frozen.txt" {
		t.Fatalf("submit retry lost its captured description or file scope: %#v", ready)
	}
}

func TestBusyCheckoutWithoutOpenFilesPreservesCapturedTarget(t *testing.T) {
	log := fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	co := &checkoutModal{cl: "41", stream: "//depot/frozen"}
	a.checkout = co
	a.opRunning, a.opName, a.opID = true, "Resolving", 7
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if unwrapActivityResult(cmd) != nil || a.checkout != co {
		t.Fatal("busy checkout lost its confirmation or started syncing")
	}
	a.Update(opEndMsg{id: 7})
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result, ok := unwrapActivityResult(cmd).(syncToCLDoneMsg)
	if !ok || result.err != nil || a.checkout != nil || !a.opRunning {
		t.Fatalf("idle checkout retry did not execute: %#v", result)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "sync -f //depot/frozen/...@41") {
		t.Fatalf("checkout retry lost captured target: %s, error %v", commands, err)
	}
}

func TestBusyMoveAndIntegrationModalsPreserveInput(t *testing.T) {
	for _, name := range []string{"move", "classic integrate", "stream integrate"} {
		t.Run(name, func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.opRunning, a.opName = true, "Resolving"
			key := tea.KeyMsg{Type: tea.KeyEnter}
			if name == "move" {
				a.moveModal = &moveCLModal{input: textinput.New(), files: []p4.OpenedFile{{ClientFile: "//workspace/frozen.txt"}}}
				a.moveModal.input.SetValue("41")
			} else if name == "classic integrate" {
				a.integrateModal = newIntegrateModalClassic()
				a.integrateModal.step = 1
				a.integrateModal.sourceInput.SetValue("//depot/source/...")
				a.integrateModal.targetInput.SetValue("//depot/target/...")
			} else {
				a.integrateModal = newIntegrateModalStream("//depot/frozen", "//depot/main")
				key = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}}
			}
			move, integrate := a.moveModal, a.integrateModal
			_, cmd := a.Update(key)
			if unwrapActivityResult(cmd) != nil || a.moveModal != move || a.integrateModal != integrate {
				t.Fatal("busy acceptance ran an action or replaced a frozen modal")
			}
			a.opRunning = false
			_, cmd = a.Update(key)
			if cmd == nil || a.moveModal != nil || a.integrateModal != nil {
				t.Fatal("idle retry did not accept the preserved form")
			}
		})
	}
}

func TestHelpResizeClampsScrollAndKeepsSemanticColors(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	a := New(&p4.Client{}, 0, "", false)
	a.showHelp = true
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	a.helpViewport.GotoBottom()
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if a.helpViewport.YOffset > max(0, a.helpViewport.TotalLineCount()-a.helpViewport.Height) {
		t.Fatal("growing the help viewport left its scroll offset beyond content")
	}
	content := a.helpContent()
	for _, color := range []string{"32|92", "36|96"} {
		if !regexp.MustCompile("\x1b\\[(?:[0-9]+;)*(?:" + color + ")(?:;[0-9]+)*m").MatchString(content) {
			t.Fatalf("help content lost semantic foreground %s", color)
		}
	}
}

func TestModalInputShrinkRecalculatesMidCaretWindow(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.width = 20
	input := textinput.New()
	input.Width = 50
	input.SetValue(strings.Repeat("特🌿abcdefghij", 10))
	input.SetCursor(25)
	input.View()
	view := a.modalInputView(input)
	if strings.Contains(view, "\n") || lipgloss.Width(view) > a.popupWidth(80)-4 {
		t.Fatalf("resized input overflows at width 20 with caret 25: width %d, %q", lipgloss.Width(view), ansi.Strip(view))
	}
	if input.Width != 50 || input.Position() != 25 || input.Value() != strings.Repeat("特🌿abcdefghij", 10) {
		t.Fatal("rendering changed the original input window, caret, or value")
	}
}

func TestBusyModalFooterKeepsProgressAndInputHints(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.width = 120
	a.modal = newSubmitModal("41", nil)
	a.opRunning, a.opName, a.opDone, a.opTotal = true, "Syncing", 3, 8
	canceled := false
	a.opCancel = func() { canceled = true }
	footer := ansi.Strip(a.renderHotkeys())
	for _, want := range []string{"Syncing", "3/8 files", "Submit: <enter>", "Close/Cancel: <esc>"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("busy modal footer lost %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "c - cancel") || lipgloss.Width(footer) > a.width {
		t.Fatalf("busy modal footer advertises unavailable cancellation or overflows: %q", footer)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if canceled || !a.opRunning || a.modal.input.Value() != "c" {
		t.Fatal("typing c in the form canceled the background operation")
	}
}
