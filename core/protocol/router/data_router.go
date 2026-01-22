package router

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"
)

type Datarouter struct{}

func NewDatarouter() *Datarouter {
	return &Datarouter{}
}

func (dr *Datarouter) HandleData(host host.Host, data proto.Message, protocol protocol.ID) error {
	fmt.Println("Data received")
	return nil
}
