package tui

import "github.com/rivo/uniseg"

const directionalMessageMinWidth = 32

// messageWrapWidth is shared by drawing and viewport measurement. Incoming
// messages keep their existing width. Outgoing blocks leave an opposite-side
// gutter of one eighth of the pane (capped at eight cells). Small panes reclaim
// the entire width rather than sacrificing readability for directionality.
func messageWrapWidth(message messageView, width int, timestamps bool) (bodyWidth, timestampPrefix int) {
	if message.fromMe && width >= directionalMessageMinWidth {
		width -= min(8, width/8)
	}
	if timestamps && width > prefixWidth {
		timestampPrefix = prefixWidth
	}
	return width - timestampPrefix, timestampPrefix
}

// messageBlockLeft aligns the block, not each individual line. Wrapping always
// uses bodyWidth; the widest rendered body/quote line determines its actual
// width, so a short send reaches the right edge instead of occupying a mostly
// empty full-width wrapper. No layout coordinates are cached across frames.
func messageBlockLeft(message messageView, left, right, bodyWidth, timestampPrefix int, quoteLine string) int {
	if !message.fromMe || right-left < directionalMessageMinWidth {
		return left
	}
	widest := uniseg.StringWidth(quoteLine)
	widest = max(widest, uniseg.StringWidth(messageReactionText(message)))
	for remaining := messageDisplayText(message); remaining != "" && widest < bodyWidth; {
		var line string
		line, remaining = nextWrappedLine(remaining, bodyWidth)
		widest = max(widest, uniseg.StringWidth(line))
	}
	return right - timestampPrefix - min(bodyWidth, widest)
}
