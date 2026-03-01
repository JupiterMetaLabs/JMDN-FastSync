package router

import (
	"fmt"
	"testing"

	router_helper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
)

func TestDivideTags(t *testing.T) {
	tags := router_helper.DivideTags(1, 10000)
	for i := range tags.Range {
		fmt.Println(tags.Range[i])
	}
}
