// Copyright (c) 2019 - for information on the respective copyright owner
// see the NOTICE file and/or the repository at
// https://github.com/direct-state-transfer/dst-go
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adapter

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/abi/bind/backends"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethereumTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/event"

	"github.com/direct-state-transfer/dst-go/ethereum/contract"
	"github.com/direct-state-transfer/dst-go/ethereum/types"
	"github.com/direct-state-transfer/dst-go/identity"
)

// Transaction wraps the transaction type defined in go-ethereum/core/types.
// It represents collection of all data corresponding to an ethereum transaction.
type Transaction struct {
	*ethereumTypes.Transaction
}

// TransactOpts wraps the TransactionOpts type defined in go-ethereum/accounts/bind/abi.
// It represents collection of authorization data required to create a valid Ethereum transaction.
type TransactOpts struct {
	*bind.TransactOpts
}

// ChainStateReader represents the ChainStateReader defined in go-ethereum.
// It defines the functions required to retrieve data at an ethereum address.
type ChainStateReader ethereum.ChainStateReader

// EventSubscription represents the Subscription interface defined in go-ethereum/event.
// It represents methods to read subscription errors and to unsubscribe.
// The carrier of events, which is usually a channel (a golang primitive) is not a part of the interface.
type EventSubscription event.Subscription

// ContractBackend wraps the ContractBackend interface defined in go-ethereum/accounts/abi/bind and also adds few methods to it.
// It represents the functions required to make a read only call, transactions that modifies any state on blockchain or a filter query.
// In addition, it also contains methods to retrieve transaction information and to know the backend type (simulated/real backend).
type ContractBackend interface {
	bind.ContractBackend
	BackendType() BackendType
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*ethereumTypes.Receipt, error)
	TransactionByHash(ctx context.Context, hash common.Hash) (tx *ethereumTypes.Transaction, isPending bool, err error)
	Commit()
}

// ContractTransactor wraps the ContractTransactor defined in go-ethereum/accounts/abi/bind.
// It represents the functions required to make transactions that modifies any state on blockchain.
type ContractTransactor bind.ContractTransactor

// ContractCaller wraps the ContractCaller defined in go-ethereum/accounts/abi/bind.
// It represents the functions required to make read only calls on blockchain.
type ContractCaller bind.ContractCaller

// ContractFilterer wraps the ContractFilterer defined in go-ethereum/accounts/abi/bind.
// It represents the functions required to filter queries on blockchain.
type ContractFilterer bind.ContractFilterer

// Log wraps the Log type defined in go-ethereum/core/types.
// It represents a contract log event on the blockchain, that is generated by the LOG opcode and is stored/indexed by the node.
type Log struct {
	ethereumTypes.Log
}

// BackendType represents the type of blockchain backend.
type BackendType string

// Enumeration of allowed values for BackendType
const (
	Real      BackendType = BackendType("realBackend")
	Simulated BackendType = BackendType("simulatedBackend")
)

// RealBackend wraps the Client type defined in go-ethereum/ethclient.
// It provides an interface to interact with the blockchain node.
type RealBackend struct {
	*ethclient.Client
}

// BackendType returns the backend typed.
func (conn *RealBackend) BackendType() BackendType {
	return Real
}

// Commit is dummy implementation to fulfill the ContractBackend interface.
func (conn *RealBackend) Commit() {
}

// SimulatedBackend wraps the SimulatedBackend type defined in go-ethereum/accounts/abi/bind/backends.
// It simulates a blockchain node for testing, without the overhead of mining.
type SimulatedBackend struct {
	*backends.SimulatedBackend
}

// BackendType returns the backend typed.
func (conn *SimulatedBackend) BackendType() BackendType {
	return Simulated
}

// TransactionByHash is a implemented for complying with types.ContractBackend interface. DO NOT USE THIS METHOD
func (conn *SimulatedBackend) TransactionByHash(ctx context.Context, hash common.Hash) (tx *ethereumTypes.Transaction, isPending bool, err error) {
	return &ethereumTypes.Transaction{}, false, fmt.Errorf("DO NOT USE THIS METHOD. Implemented for ContractBackend interface")
}

// NewRealBackend initializes and returns an blockchain instance with a connection to the blockchain node running at nodeUrl.
func NewRealBackend(nodeURL string) (*RealBackend, error) {
	conn, err := ethclient.Dial(nodeURL)
	if err != nil {
		return nil, err
	}
	return &RealBackend{conn}, nil
}

// NewSimulatedBackend initializes and returns an blockchain instance with a simulated backend.
// It creates two dummy accounts with address and balances (in Wei) as specified in balanceList
func NewSimulatedBackend(balanceList map[types.Address]*big.Int) *SimulatedBackend {

	genesisAlloc := make(core.GenesisAlloc)

	for accountAddr, balance := range balanceList {
		accountAddrEth := accountAddr.Address
		genesisAlloc[accountAddrEth] = core.GenesisAccount{
			Balance: balance,
		}
	}

	//Gas limit used in genesis block - 0x8000000 = 134217728
	//Closest multiple of 10 is 1e8
	//TODO : Move this to a variable in config of blockchain module
	return &SimulatedBackend{backends.NewSimulatedBackend(genesisAlloc, uint64(1e8))}
}

// MakeFilterOpts makes a FilterOpts object with provided values.
// This will be used when making a filter query on the blockchain.
func MakeFilterOpts(context context.Context, startBlock, endBlock *uint64) *bind.FilterOpts {

	return &bind.FilterOpts{
		Start:   *startBlock,
		End:     endBlock,
		Context: context,
	}
}

// MakeCallOpts makes a CallOpts object with provided values.
// This will be used when making a read only function calls on the blockchain.
func MakeCallOpts(context context.Context, includePending bool, address types.Address) *bind.CallOpts {

	return &bind.CallOpts{
		Pending: includePending,
		From:    address.Address,
		Context: context,
	}
}

// MakeTransactOpts makes a TransactOpts object with provided values.
// This will be used when submitting a transaction to the blockchain.
func MakeTransactOpts(conn ContractTransactor, idWithCredentials identity.OffChainID, valueInWei *big.Int, gasLimit uint64) (transactOpts *TransactOpts, err error) {

	ks, password, isSetCredentials := idWithCredentials.GetCredentials()

	if !isSetCredentials {
		return nil, fmt.Errorf("Credentials not set in identity")
	}
	defer idWithCredentials.ClearCredentials()

	accountAddr := idWithCredentials.OnChainID
	key, err := identity.GetKey(ks, accountAddr, password)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	nonce, err := conn.PendingNonceAt(ctx, accountAddr.Address)
	if err != nil {
		return nil, err
	}
	gasPrice, err := conn.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	//Private key is fetched from identity module because, that way
	//both per transaction unlock and timed unlock of accounts can be supported
	transactOptsEth := bind.NewKeyedTransactor(key.PrivateKey)
	transactOptsEth.Nonce = big.NewInt(int64(nonce))
	transactOptsEth.Value = valueInWei
	transactOptsEth.GasLimit = gasLimit //	in	units
	transactOptsEth.GasPrice = gasPrice

	transactOpts = &TransactOpts{TransactOpts: transactOptsEth}

	return transactOpts, err
}

// DeployContract deploys the contract represented by handle with parameters as defined by params.
// It initializes and returns a handler that is bound to the instance of the deployed contract.
// For the deploy transaction will be send from the onchain id in idWithCredentials.
func DeployContract(handle contract.Handler, conn ContractBackend, params []interface{}, idWithCredentials identity.OffChainID) (
	contractAddr types.Address, tx *Transaction, handler interface{}, err error) {

	contractAddr, tx, handler, err = deployContract(handle, conn, params, idWithCredentials)
	if err != nil {
		return contractAddr, tx, handler, err
	}
	hash := types.Hash{Hash: tx.Hash()}

	_, err = WaitTillTxMined(conn, hash)

	return contractAddr, tx, handler, err
}

func deployContract(handle contract.Handler, conn ContractBackend, params []interface{}, idWithCredentials identity.OffChainID) (
	contractAddr types.Address, tx *Transaction, handler interface{}, err error) {

	transactOpts, err := MakeTransactOpts(conn, idWithCredentials, big.NewInt(0), handle.GasUnits)
	if err != nil {
		return contractAddr, tx, handler, err
	}

	var contractAddrEth common.Address
	var txEth *ethereumTypes.Transaction

	switch handle.Name {
	case "LibSignatures":
		contractAddrEth, txEth, handler, err = contract.DeployLibSignatures(transactOpts.TransactOpts, conn)
	case "MSContract":
		if len(params) != 3 {
			err = fmt.Errorf("Insuffcient number of parameters. Want 3")
			return contractAddr, tx, handler, err
		}
		libAddr, ok := params[0].(types.Address)
		if !ok {
			err = fmt.Errorf("Cannot derive libSignature address from params[0]")
			return contractAddr, tx, handler, err
		}
		aliceAddr, ok := params[1].(types.Address)
		if !ok {
			err = fmt.Errorf("Cannot derive user1 address from params[1]")
			return contractAddr, tx, handler, err
		}
		bobAddr, ok := params[2].(types.Address)
		if !ok {
			err = fmt.Errorf("Cannot derive user2 address from params[2]")
			return contractAddr, tx, handler, err
		}
		contractAddrEth, txEth, handler, err = contract.DeployMSContract(transactOpts.TransactOpts, conn, libAddr.Address, aliceAddr.Address, bobAddr.Address)
	case "VPC":
		if len(params) != 1 {
			err = fmt.Errorf("Insuffcient number of parameters. Want 1")
			return contractAddr, tx, handler, err
		}
		libAddr, ok := params[0].(types.Address)
		if !ok {
			err = fmt.Errorf("Cannot derive libSignature address from params[0]")
			return contractAddr, tx, handler, err
		}
		contractAddrEth, txEth, handler, err = contract.DeployVPC(transactOpts.TransactOpts, conn, libAddr.Address)
	default:
		err = fmt.Errorf("Invalid contract received")
		return contractAddr, tx, handler, err
	}

	contractAddr = types.Address{Address: contractAddrEth}
	tx = &Transaction{Transaction: txEth}

	return contractAddr, tx, handler, err
}

// WaitTillTxMined returns the time elapsed when the contract at txHash has been successfully mined.
// Incase of simulated backend (in tests / walkthrough) is used instead of connection with a blockchain node,
// it returns immediately as there is no mining involved.
func WaitTillTxMined(conn ContractBackend, txHash types.Hash) (duration time.Duration, err error) {

	switch conn.BackendType() {
	case Real:
		tsStart := time.Now()
		//TODO : Provide a timeout mechanism for this loop to exit
		for {
			var isPending bool
			_, isPending, err = conn.TransactionByHash(context.Background(), txHash.Hash)
			if err != nil {
				return
			}
			if !isPending {
				break
			}
			time.Sleep(500 * time.Millisecond) //Check every 500 ms
		}
		duration = time.Since(tsStart)
		return duration, nil

	case Simulated:
		return duration, nil

	default:
		return duration, fmt.Errorf("Invalid connection object")
	}

}

// VerifyCodeAt checks if the contract code at contractAddr is same as the one represented by hashBinRuntime.
// If the contract is a library (as per solidity terms), set isLibrary to true, else set it to false.
func VerifyCodeAt(contractAddr types.Address, hashBinRuntime string, isLibrary bool, conn ContractBackend) (
	matchStatus contract.MatchStatus, err error) {

	contractAddrEth := contractAddr.Address

	code, err := conn.CodeAt(context.Background(), contractAddrEth, nil)
	if err != nil {
		return contract.Unknown, err
	}

	if len(code) == 0 {
		err = fmt.Errorf("Code at given address is empty")
		return contract.Unknown, err
	}

	//In case of Library, first 21 bytes of bin-runtime will be 0.
	//After deployment, these 21 bytes will be set to the address at which it is deployed
	//Hence reset these bytes before comparison
	if isLibrary {
		for idx := 1; idx < 21; idx++ {
			code[idx] = byte(0)
		}
	}
	code = []byte(fmt.Sprintf("%x", code))

	codeBuff := bytes.NewBuffer(code)
	matchStatus, err = contract.CheckSha256Sum(codeBuff, hashBinRuntime)
	if err != nil {
		return contract.Unknown, err
	}
	return matchStatus, err
}
