package tagging

import (
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
)

type Tagging struct {
	Tag        *taggingpb.Tag
	AccountTag *taggingpb.TaggedAccounts
}

func NewTagging() *Tagging {
	return &Tagging{
		Tag: &taggingpb.Tag{},
		AccountTag: &taggingpb.TaggedAccounts{},
	}
}

func (t *Tagging) TagBlock(block uint64) *taggingpb.Tag {
	// append the block number to the tag
	t.Tag.BlockNumber = append(t.Tag.BlockNumber, block)
	return t.Tag
}

func (t *Tagging) TagRange(start uint64, end uint64) *taggingpb.Tag {
	// append the range to the tag
	newtag := &taggingpb.RangeTag{Start: start, End: end}

	t.Tag.Range = append(t.Tag.Range, newtag)
	return t.Tag
}

func (t *Tagging) TagAccounts(account string) Tagging {
	if t.AccountTag == nil {
		t.AccountTag = &taggingpb.TaggedAccounts{}
	}
	if t.AccountTag.Accounts == nil {
		t.AccountTag.Accounts = make(map[string]bool)
	}
	t.AccountTag.Accounts[account] = true
	return *t
}

func (t *Tagging) GetAccountTag() *taggingpb.TaggedAccounts {
	return t.AccountTag
}

func (t *Tagging) GetAccount(key string) bool {
	return t.AccountTag.Accounts[key]
}