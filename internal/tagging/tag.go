package tagging

import "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"

type Tag struct {
	BlockNumber []uint64
	Range       []*merkle.Range
}

type Tagging struct {
	Tag *Tag
}

func NewTagging() *Tagging {
	return &Tagging{
		Tag: &Tag{
			BlockNumber: []uint64{},
			Range:       []*merkle.Range{},
		},
	}
}

func (t *Tagging) TagRange(start, end uint64) {
	t.Tag.Range = append(t.Tag.Range, &merkle.Range{
		Start: start,
		End:   end,
	})
}
