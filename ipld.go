package hamt

import (
	"context"
	"math"
	"time"
	"fmt"

	/*
		bstore "github.com/ipfs/go-ipfs/blocks/blockstore"
		bserv "github.com/ipfs/go-ipfs/blockservice"
		offline "github.com/ipfs/go-ipfs/exchange/offline"
	*/

	block "github.com/ipfs/go-block-format"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	cbor "github.com/ipfs/go-ipld-cbor"
	recbor "github.com/polydawn/refmt/cbor"
	atlas "github.com/polydawn/refmt/obj/atlas"
	ds "github.com/ipfs/go-datastore"
	cid "github.com/ipfs/go-cid"
)

// THIS IS ALL TEMPORARY CODE
// ALL SOFTWARE IS TEMPORARY!!

var sessionCount map[string]int

func init() {
	cbor.RegisterCborType(cbor.BigIntAtlasEntry)
	cbor.RegisterCborType(Node{})
	cbor.RegisterCborType(Pointer{})

	kvAtlasEntry := atlas.BuildEntry(KV{}).Transform().TransformMarshal(
		atlas.MakeMarshalTransformFunc(func(kv KV) ([]interface{}, error) {
			return []interface{}{kv.Key, kv.Value}, nil
		})).TransformUnmarshal(
		atlas.MakeUnmarshalTransformFunc(func(v []interface{}) (KV, error) {
			return KV{
				Key:   v[0].(string),
				Value: v[1],
			}, nil
		})).Complete()
	cbor.RegisterCborType(kvAtlasEntry)

	sessionCount = make(map[string]int)
	sessionCount["adds"] = 0
	sessionCount["biggest"] = 0	
}

type CborIpldStore struct {
	Blocks blocks
	Atlas  *atlas.Atlas
}

type blocks interface {
	GetBlock(context.Context, cid.Cid) (block.Block, error)
	AddBlock(block.Block) error
	Blockstore() blockstore.Blockstore
}

type mockBlocks struct {
	data map[cid.Cid][]byte
}

func newMockBlocks() *mockBlocks {
	return &mockBlocks{make(map[cid.Cid][]byte)}
}

func (mb *mockBlocks) GetBlock(ctx context.Context, cid cid.Cid) (block.Block, error) {
	d, ok := mb.data[cid]
	if ok {
		return block.NewBlock(d), nil
	}
	return nil, ErrNotFound
}

func (mb *mockBlocks) AddBlock(b block.Block) error {
	mb.data[b.Cid()] = b.RawData()
	return nil
}

func (mb *mockBlocks) Blockstore() blockstore.Blockstore {
	return blockstore.NewBlockstore(ds.NewMapDatastore())
}

func NewCborStore() *CborIpldStore {
	return &CborIpldStore{Blocks: newMockBlocks()}
}

func (s *CborIpldStore) Get(ctx context.Context, c cid.Cid, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	blk, err := s.Blocks.GetBlock(ctx, c)
	if err != nil {
		return err
	}

	if s.Atlas == nil {
		return cbor.DecodeInto(blk.RawData(), out)
	} else {
		return recbor.UnmarshalAtlased(recbor.DecodeOptions{}, blk.RawData(), out, *s.Atlas)
	}
}

type cidProvider interface {
	Cid() cid.Cid
}

func (s *CborIpldStore) Put(ctx context.Context, v interface{}) (cid.Cid, error) {
	mhType := uint64(math.MaxUint64)
	mhLen := -1
	if c, ok := v.(cidProvider); ok {
		pref := c.Cid().Prefix()
		mhType = pref.MhType
		mhLen = pref.MhLength
	}

	nd, err := cbor.WrapObject(v, mhType, mhLen)
	if err != nil {
		return cid.Cid{}, err
	}

	// Don't add up storage if already stored.
	if has, err := s.Blocks.Blockstore().Has(nd.Cid()); err != nil && has {
		return nd.Cid(), nil
	}

	if err := s.Blocks.AddBlock(nd); err != nil {
		return cid.Cid{}, err
	}

	// Record bytes for this invocation
	id, ok := ctx.Value("flush-point").(string)
	if id != "" {
		if !ok {
			panic("flush-point is not a string, weird")
		}
		putBytes := len(nd.RawData())
		if _, ok := sessionCount[id]; !ok {
			sessionCount[id] = 0
		}
		sessionCount[id] += putBytes
		sessionCount["adds"] += 1
		if putBytes > sessionCount["biggest"] {
			sessionCount["biggest"] = putBytes
		}

		jsonBytes, _ := nd.MarshalJSON()
		fmt.Printf("ipld store node:\ncid:%s\nsize:%d\n%s\n", nd.Cid().String(), putBytes, string(jsonBytes))
	}
	
	return nd.Cid(), nil
}

func (s *CborIpldStore) SessionCount(id string) int {
	count, ok := sessionCount[id]
	if !ok {
		return 0
	}
	return count
}
