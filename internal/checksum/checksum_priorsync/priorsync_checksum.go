package checksum_priorsync

import (
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/merkle"
	pb "github.com/JupiterMetaLabs/JMDN-FastSync/internal/proto/priorsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	"google.golang.org/protobuf/proto"
)

type Checksum struct{}

func PriorSyncChecksum() *Checksum {
	return &Checksum{}
}

func (c *Checksum) check_version(version uint16) bool {
	if version == 1 || version == 2 {
		return true
	}
	return false
}

func (c *Checksum) Create(data types.PriorSync, version uint16) ([]byte, error) {
	if !c.check_version(version) {
		return nil, errors.UnsupportedChecksumVersion
	}
	pbData := &pb.PriorSync{
		Blocknumber:    data.Blocknumber,
		Stateroot:      data.Stateroot,
		Blockhash:      data.Blockhash,
		Merklesnapshot: &merkle.MerkleSnapshot{},
		Metadata:       &pb.Metadata{},
	}
	if data.Range != nil {
		pbData.Range = &merkle.Range{
			Start: data.Range.Start,
			End:   data.Range.End,
		}
	}
	// No need to give the metadata for creating the checksum

	dataBytes, err := proto.Marshal(pbData)
	if err != nil {
		return nil, err
	}
	return checksum.NewChecksum().Create(dataBytes, version)
}

func (c *Checksum) Verify(data types.PriorSync, version uint16, expected []byte) (bool, error) {
	if !c.check_version(version) {
		return false, errors.UnsupportedChecksumVersion
	}
	pbData := &pb.PriorSync{
		Blocknumber:    data.Blocknumber,
		Stateroot:      data.Stateroot,
		Blockhash:      data.Blockhash,
		Merklesnapshot: &merkle.MerkleSnapshot{},
		Metadata:       &pb.Metadata{},
	}
	if data.Range != nil {
		pbData.Range = &merkle.Range{
			Start: data.Range.Start,
			End:   data.Range.End,
		}
	}
	dataBytes, err := proto.Marshal(pbData)
	if err != nil {
		return false, err
	}
	return checksum.NewChecksum().Verify(dataBytes, version, expected)
}

func (c *Checksum) CreatefromPB(data *pb.PriorSync, version uint16) ([]byte, error) {
	if !c.check_version(version) {
		return nil, errors.UnsupportedChecksumVersion
	}
	pbData := &pb.PriorSync{
		Blocknumber:    data.Blocknumber,
		Stateroot:      data.Stateroot,
		Blockhash:      data.Blockhash,
		Merklesnapshot: &merkle.MerkleSnapshot{},
		Metadata:       &pb.Metadata{},
	}
	if data.Range != nil {
		pbData.Range = &merkle.Range{
			Start: data.Range.Start,
			End:   data.Range.End,
		}
	}
	// No need to give the metadata for creating the checksum

	dataBytes, err := proto.Marshal(pbData)
	if err != nil {
		return nil, err
	}
	return checksum.NewChecksum().Create(dataBytes, version)
}

func (c *Checksum) VerifyfromPB(data *pb.PriorSync, version uint16, expected []byte) (bool, error) {
	priorSyncData := types.PriorSync{
		Blocknumber: data.Blocknumber,
		Stateroot:   data.Stateroot,
		Blockhash:   data.Blockhash,
		Metadata:    types.Metadata{},
	}
	if data.Range != nil {
		priorSyncData.Range = &types.Range{
			Start: data.Range.Start,
			End:   data.Range.End,
		}
	}
	return c.Verify(priorSyncData, version, expected)
}
