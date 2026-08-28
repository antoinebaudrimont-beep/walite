package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

func formatChatRow(title string, unread uint32, selected bool, width int) string {
	if width <= 0 {
		return ""
	}
	prefix := "  "
	if selected {
		prefix = "> "
	}
	if width < len(prefix) {
		return prefix[:width]
	}

	available := width - len(prefix)
	suffix := formatUnreadSuffix(unread, available)
	titleWidth := available - uniseg.StringWidth(suffix)
	return prefix + truncateDisplayWidth(title, titleWidth) + suffix
}

func formatUnreadSuffix(unread uint32, width int) string {
	if unread == 0 || width <= 0 {
		return ""
	}
	digits := strconv.Itoa(int(unread))
	for _, candidate := range []string{" (" + digits + ")", "(" + digits + ")", digits} {
		if len(candidate) <= width {
			return candidate
		}
	}
	return digits[:width]
}

func truncateDisplayWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	if uniseg.StringWidth(value) <= width {
		return value
	}

	const ellipsis = "…"
	remaining := width - uniseg.StringWidth(ellipsis)
	if remaining <= 0 {
		return ellipsis
	}
	var result strings.Builder
	graphemes := uniseg.NewGraphemes(value)
	used := 0
	for graphemes.Next() {
		clusterWidth := graphemes.Width()
		if used+clusterWidth > remaining {
			break
		}
		result.WriteString(graphemes.Str())
		used += clusterWidth
	}
	result.WriteString(ellipsis)
	return result.String()
}
