package example

import (
	"math/big"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/peer"
)

type example_blockinfo struct{}

func NewExampleBlockInfo() types.BlockInfo {
	return example_blockinfo{}
}

func (e example_blockinfo) NewAccountNonceIterator(batchSize int) types.AccountNonceIterator {
	return &example_accountnonceiterator{}
}

type example_accountnonceiterator struct{}

func (e example_accountnonceiterator) NextBatch() ([]*types.Account, error) {
	return nil, nil
}

func (e example_accountnonceiterator) TotalAccounts() (uint64, error) {
	return 0, nil
}

func (e example_accountnonceiterator) GetAccountsByNonces(nonces []uint64) ([]*types.Account, error) {
	return nil, nil
}

func (e example_accountnonceiterator) Close() {}

type example_authhandler struct{}

func (e example_blockinfo) AUTH() types.AUTHHandler {
	return &example_authhandler{}
}

func (e example_authhandler) AddRecord(peerID peer.ID, UUID string) error {
	return nil
}

func (e example_authhandler) RemoveRecord(peerID peer.ID) error {
	return nil
}

func (e example_authhandler) GetRecord(peerID peer.ID) (types.AUTHStructure, error) {
	return types.AUTHStructure{}, nil
}

func (e example_authhandler) IsAUTH(peerID peer.ID, UUID string) (bool, error){
	return false, nil
}

func (e example_authhandler) ResetTTL(peerID peer.ID) error {
	return nil
}

func (e example_blockinfo) GetBlockNumber() uint64 {
	return 100
}

func (e example_blockinfo) GetBlockDetails() types.PriorSync {
	return GetBlockDetailsDummy()
}

func (e example_blockinfo) NewBlockIterator(start, end uint64, batchsize int) types.BlockIterator {
	return &example_iterator{}
}

type example_iterator struct{}

func (i *example_iterator) Next() ([]*types.ZKBlock, error) {
	return nil, nil
}

func (i *example_iterator) Prev() ([]*types.ZKBlock, error) {
	return nil, nil
}

func (i *example_iterator) Close() {}

type example_blockheader struct{}

func (e example_blockinfo) NewBlockHeaderIterator() types.BlockHeader {
	return example_blockheader{}
}

func (e example_blockheader) GetBlockHeaders(blocknumbers []uint64) ([]*block.Header, error) {
	return nil, nil
}

func (e example_blockheader) GetBlockHeadersRange(start, end uint64) ([]*block.Header, error) {
	return nil, nil
}

type example_writeheaders struct{}

func (e example_blockinfo) NewHeadersWriter() types.WriteHeaders {
	return example_writeheaders{}
}

func (e example_writeheaders) WriteHeaders(headers []*block.Header) error {
	return nil
}

type example_writedata struct{}

func (e example_blockinfo) NewDataWriter() types.WriteData {
	return example_writedata{}
}

func (e example_writedata) WriteData(data []*block.NonHeaders) error {
	return nil
}	

type example_blocknonheader struct{}

func (e example_blockinfo) NewBlockNonHeaderIterator() types.BlockNonHeader {
	return example_blocknonheader{}
}

func (e example_blocknonheader) GetBlockNonHeaders(blocknumbers []uint64) ([]*block.NonHeaders, error) {
	return nil, nil
}

func (e example_blocknonheader) GetBlockNonHeadersRange(start, end uint64) ([]*block.NonHeaders, error) {
	return nil, nil
}

type example_accountmanager struct{}

func (e example_blockinfo) NewAccountManager() types.AccountManager {
	return &example_accountmanager{}
}

func (e *example_accountmanager) NewAccountNonceIterator(batchSize int) types.AccountNonceIterator {
	return &example_accountnonceiterator{}
}

func (e example_accountmanager) GetAccountBalance(address string) (*big.Int, uint64, error) {
	return nil, 0, nil
}

func (e example_accountmanager) GetTransactionsForAccount(accountAddress string) ([]types.DBTransaction, error) {
	return nil, nil
}

func (e example_accountmanager) CreateAccount(address string, balance *big.Int, nonce uint64) error {
	return nil
}

func (e example_accountmanager) UpdateAccountBalance(address string, balance *big.Int, nonce uint64) error {
	return nil
}

func (e example_accountmanager) BatchUpdateAccounts(updates []types.AccountUpdate) error {
	return nil
}




func GetBlockDetailsDummy() types.PriorSync {
	data := types.PriorSync{
		Blocknumber: 100,
		Stateroot:   []byte("example-stateroot"),
		Blockhash:   []byte("example-blockhash"),
		Metadata: types.Metadata{
			Version: 1,
		},
	}
	return data
}
