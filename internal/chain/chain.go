// Package chain gives the backend a read-only view of WordBreakPools, so the referee can
// verify that a player actually paid into a round before scoring them or paying them out.
//
// Why this exists: the contract pays whatever addresses the referee signs. If the backend
// scored (and later paid) any address that just POSTs words, an attacker who never called
// enter() could be signed as the winner and drain honest players' entry fees. Gating every
// paid submission on hasEntered(roundId, addr) closes that hole. Reads are per-address, so we
// never hit the eth_getLogs 50k-block range limit.
package chain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Minimal ABI: just the read-only getters the referee needs.
const poolABIJSON = `[
  {"type":"function","name":"hasEntered","stateMutability":"view",
   "inputs":[{"name":"","type":"uint256"},{"name":"","type":"address"}],
   "outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"roundExists","stateMutability":"view",
   "inputs":[{"name":"","type":"uint256"}],
   "outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"getRound","stateMutability":"view",
   "inputs":[{"name":"","type":"uint256"}],
   "outputs":[
     {"name":"entryFee","type":"uint128"},
     {"name":"endTime","type":"uint64"},
     {"name":"refundDelay","type":"uint32"},
     {"name":"rakeBps","type":"uint16"},
     {"name":"settled","type":"bool"},
     {"name":"cancelled","type":"bool"},
     {"name":"pot","type":"uint128"},
     {"name":"entrants","type":"uint32"}
   ]}
]`

// Round mirrors WordBreakPools' on-chain Round struct -- see contracts/src/WordBreakPools.sol.
type Round struct {
	EntryFee    *big.Int
	EndTime     uint64
	RefundDelay uint32
	RakeBps     uint16
	Settled     bool
	Cancelled   bool
	Pot         *big.Int
	Entrants    uint32
}

// Client is a read-only binding to a deployed WordBreakPools.
type Client struct {
	bound *bind.BoundContract
	eth   *ethclient.Client
}

// New dials the RPC and binds to the pool at contractAddr.
func New(rpcURL, contractAddr string) (*Client, error) {
	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid contract address: %q", contractAddr)
	}
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", rpcURL, err)
	}
	parsed, err := abi.JSON(strings.NewReader(poolABIJSON))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}
	bound := bind.NewBoundContract(common.HexToAddress(contractAddr), parsed, eth, eth, eth)
	return &Client{bound: bound, eth: eth}, nil
}

// HasEntered reports whether addr paid into the given round.
func (c *Client) HasEntered(ctx context.Context, roundID *big.Int, addr common.Address) (bool, error) {
	var out []interface{}
	if err := c.bound.Call(&bind.CallOpts{Context: ctx}, &out, "hasEntered", roundID, addr); err != nil {
		return false, fmt.Errorf("hasEntered: %w", err)
	}
	entered, ok := out[0].(bool)
	if !ok {
		return false, fmt.Errorf("hasEntered: unexpected return type")
	}
	return entered, nil
}

// RoundExists reports whether a round has been opened on-chain.
func (c *Client) RoundExists(ctx context.Context, roundID *big.Int) (bool, error) {
	var out []interface{}
	if err := c.bound.Call(&bind.CallOpts{Context: ctx}, &out, "roundExists", roundID); err != nil {
		return false, fmt.Errorf("roundExists: %w", err)
	}
	exists, _ := out[0].(bool)
	return exists, nil
}

// GetRound reads a round's on-chain state -- used to settle with the contract's actual pot and
// rake rather than recomputing them locally, which could drift from what's really escrowed.
func (c *Client) GetRound(ctx context.Context, roundID *big.Int) (*Round, error) {
	var out []interface{}
	if err := c.bound.Call(&bind.CallOpts{Context: ctx}, &out, "getRound", roundID); err != nil {
		return nil, fmt.Errorf("getRound: %w", err)
	}
	if len(out) != 8 {
		return nil, fmt.Errorf("getRound: unexpected output count %d", len(out))
	}
	entryFee, ok := out[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected entryFee type")
	}
	endTime, ok := out[1].(uint64)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected endTime type")
	}
	refundDelay, ok := out[2].(uint32)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected refundDelay type")
	}
	rakeBps, ok := out[3].(uint16)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected rakeBps type")
	}
	settled, ok := out[4].(bool)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected settled type")
	}
	cancelled, ok := out[5].(bool)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected cancelled type")
	}
	pot, ok := out[6].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected pot type")
	}
	entrants, ok := out[7].(uint32)
	if !ok {
		return nil, fmt.Errorf("getRound: unexpected entrants type")
	}
	return &Round{
		EntryFee: entryFee, EndTime: endTime, RefundDelay: refundDelay, RakeBps: rakeBps,
		Settled: settled, Cancelled: cancelled, Pot: pot, Entrants: entrants,
	}, nil
}

// Close releases the RPC connection.
func (c *Client) Close() { c.eth.Close() }
