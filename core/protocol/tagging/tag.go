package tagging

import (
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/tagging"
)

type Tagging struct {
	Tag *tagging.Tag
}

func NewTagging() *Tagging {
	return &Tagging{
		Tag: &tagging.Tag{},
	}
}

func (t *Tagging) TagBlock(block uint64) *tagging.Tag {
	// append the block number to the tag
	t.Tag.BlockNumber = append(t.Tag.BlockNumber, block)
	return t.Tag
}

func (t *Tagging) TagRange(start uint64, end uint64) *tagging.Tag {
	// append the range to the tag
	newtag := &tagging.RangeTag{Start: start, End: end}

	t.Tag.Range = append(t.Tag.Range, newtag)
	return t.Tag
}
