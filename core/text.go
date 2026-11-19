package core

import (
	"strings"

	"github.com/golem-mc/golem/plugin"
)

// LPPrefix returns LuckPerms's standard "[LP] " prefix component.
func LPPrefix() plugin.Text {
	return plugin.Plain("[").Color("gray").Append(
		plugin.Plain("LP").Color("aqua").Bold(),
		plugin.Plain("] ").Color("gray"),
	)
}

// Prefixed builds an "[LP] " prefixed message.
func Prefixed(children ...plugin.Text) plugin.Text {
	return LPPrefix().Append(children...)
}

// SendPrefixed sends an "[LP] " prefixed message to sender.
func SendPrefixed(sender plugin.CommandSender, children ...plugin.Text) {
	if sender != nil {
		sender.SendMessage(Prefixed(children...))
	}
}

// SendInfo sends a green informational message with "[LP] " prefix.
func SendInfo(sender plugin.CommandSender, msg string) {
	SendPrefixed(sender, plugin.Plain(msg).Color("green"))
}

// SendError sends a red error message with "[LP] " prefix.
func SendError(sender plugin.CommandSender, msg string) {
	SendPrefixed(sender, plugin.Plain(msg).Color("red"))
}

// FormatTristate formats a Tristate with LuckPerms's green/red/gray colors.
func FormatTristate(t Tristate) plugin.Text {
	switch t {
	case TristateTrue:
		return plugin.Plain("true").Color("green")
	case TristateFalse:
		return plugin.Plain("false").Color("red")
	default:
		return plugin.Plain("undefined").Color("gray")
	}
}

// FormatBool formats a bool with green/red color.
func FormatBool(b bool) plugin.Text {
	if b {
		return plugin.Plain("true").Color("green")
	}
	return plugin.Plain("false").Color("red")
}

// FormatContexts formats a ContextSet as "(k=v, ...)" or empty if global.
func FormatContexts(cs ContextSet) plugin.Text {
	if cs.IsEmpty() {
		return plugin.Plain("")
	}
	entries := cs.Entries()
	out := plugin.Plain(" (").Color("gray")
	for i, e := range entries {
		if i > 0 {
			out = out.Append(plugin.Plain(", ").Color("gray"))
		}
		out = out.Append(
			plugin.Plain(e.Key).Color("dark_aqua"),
			plugin.Plain("=").Color("gray"),
			plugin.Plain(e.Value).Color("aqua"),
		)
	}
	return out.Append(plugin.Plain(")").Color("gray"))
}

// ParseLegacyColor converts '&' or '§' Minecraft color codes into a plugin.Text component.
func ParseLegacyColor(s string) plugin.Text {
	if !strings.ContainsAny(s, "&§") {
		return plugin.Plain(s)
	}
	root := plugin.Plain("")
	var buf strings.Builder
	var style plugin.Style

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		t := plugin.Text{Literal: buf.String(), Style: style}
		root = root.Append(t)
		buf.Reset()
	}

	for i := 0; i < len(s); i++ {
		if (s[i] == '&' || s[i] == 0xc2) && i+1 < len(s) {
			var code byte
			step := 0
			if s[i] == '&' {
				code = s[i+1]
				step = 1
			} else if s[i] == 0xc2 && s[i+1] == 0xa7 && i+2 < len(s) { // UTF-8 '§'
				code = s[i+2]
				step = 2
			}
			if step > 0 {
				if c, ok := legacyColorName(code); ok {
					flush()
					style = plugin.Style{Color: c}
					i += step
					continue
				}
				switch code | 0x20 {
				case 'l':
					flush()
					style.Bold = new(true)
					i += step
					continue
				case 'o':
					flush()
					style.Italic = new(true)
					i += step
					continue
				case 'n':
					flush()
					style.Underlined = new(true)
					i += step
					continue
				case 'm':
					flush()
					style.Strikethrough = new(true)
					i += step
					continue
				case 'k':
					flush()
					style.Obfuscated = new(true)
					i += step
					continue
				case 'r':
					flush()
					style = plugin.Style{}
					i += step
					continue
				}
			}
		}
		buf.WriteByte(s[i])
	}
	flush()
	return root
}

func legacyColorName(code byte) (string, bool) {
	switch code | 0x20 {
	case '0':
		return "black", true
	case '1':
		return "dark_blue", true
	case '2':
		return "dark_green", true
	case '3':
		return "dark_aqua", true
	case '4':
		return "dark_red", true
	case '5':
		return "dark_purple", true
	case '6':
		return "gold", true
	case '7':
		return "gray", true
	case '8':
		return "dark_gray", true
	case '9':
		return "blue", true
	case 'a':
		return "green", true
	case 'b':
		return "aqua", true
	case 'c':
		return "red", true
	case 'd':
		return "light_purple", true
	case 'e':
		return "yellow", true
	case 'f':
		return "white", true
	default:
		return "", false
	}
}
