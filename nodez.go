package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcrpcclient"
	"github.com/btcsuite/btcutil"
	"github.com/davecheney/profile"
	log "github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	port          = flag.String("port", "8080", "Port to listen on")
	nodeAddr      = flag.String("node_addr", ":8333", "Bitcoin node address")
	bitcoinConf   = flag.String("bitcoin_conf", "~/.bitcoin/bitcoin.conf", "Bitcoin configuration file")
	debugLog      = flag.String("debug_log", "~/.bitcoin/debug.log", "bitcoind debug log")
	staticDir     = flag.String("static_dir", ".", "Path to static files")
	templatesPath = flag.String("templates_path", "templates", "Path to templates")
	testnet       = flag.Bool("testnet", false, "Connect to testnet")
	useIP         = flag.String("use_ip", "", "Use this IP address instead of querying myexternalip.com")
	fakeStream    = flag.Bool("fake_stream", false, "Send fake data (for testing)")
	prof          = flag.String("prof", "", "If non-empty, run profileer and output to this file")
	bitcoinCLI    = flag.String("bitcoin_cli", "", "If non-empty, use this bitcoin-cli binary for bitcoind JSON-RPC not supported by github.com/btcsuite/btcrpclient")

	bitcoindUpdateTipRE = regexp.MustCompile(
		`UpdateTip:.*best=([0-9a-f]+).*height=(\d+).*tx=(\d+).*date=(.*) progress`)
	bitcoindDateLayout = "2006-01-02 15:04:05"

	rpcClient *btcrpcclient.Client

	latestInfoJSON InfoJSON

	myIP net.IP

	isSyncing = false
)

const dateFormat = "2006-01-02 3:04:05"

func main() {
	flag.Parse()

	if *useIP != "" {
		myIP = net.ParseIP(*useIP)
	} else {
		resp, err := http.Get("http://myexternalip.com/raw")
		if err != nil {
			log.Fatal(err)
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		myIP = net.ParseIP(strings.TrimSpace(string(body)))
		resp.Body.Close()
	}
	log.Infof("My IP: %s", myIP.String())

	*bitcoinConf = strings.Replace(*bitcoinConf, "~", os.Getenv("HOME"), -1)
	*debugLog = strings.Replace(*debugLog, "~", os.Getenv("HOME"), -1)

	connCfg := &btcrpcclient.ConnConfig{
		Host:         "localhost:8332",
		HttpPostMode: true, // Bitcoin core only supports HTTP POST mode
		DisableTLS:   true, // Bitcoin core does not provide TLS by default
	}

	f, err := os.Open(*bitcoinConf)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		t := scanner.Text()
		arr := strings.Split(t, "=")
		if len(arr) != 2 {
			continue
		}
		switch arr[0] {
		case "rpcuser":
			connCfg.User = arr[1]
		case "rpcpassword":
			connCfg.Pass = arr[1]
		}
	}

	rpcClient, err = btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Get the current block count.
	// TODO(ortutay): if error, probably syncing
	blockCount, err := rpcClient.GetBlockCount()
	if err != nil {
		log.Error(err)
	} else {
		log.Infof("Block count: %v", blockCount)
	}

	// txHash, err := wire.NewShaHashFromStr("752140443f73bc6ed58623d28c82393682f1895ea8ce8aec53ca00f847342a50")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// tx, err := rpcClient.GetTransaction(txHash)
	// log.Infof("tx: %v %v", tx, err)
	// return

	var btcnet wire.BitcoinNet
	if *testnet {
		btcnet = wire.TestNet3
	} else {
		btcnet = wire.MainNet
	}

	if *fakeStream {
		go fakeBitcoinStream()
	} else {
		go bitcoinStream(wire.ProtocolVersion, btcnet)
	}
	go bitcoindDebugLogStream(*debugLog)
	go updateNodeInfo()

	r := mux.NewRouter()
	mux := http.NewServeMux()

	r.Handle("/nodez/wire/stream", Endpoint{Serve: handleWireStream})
	r.Handle("/nodez/info/json", Endpoint{Serve: handleInfoJSON})
	r.Handle("/nodez/info/stream", Endpoint{Serve: handleInfoStream})
	r.Handle("/nodez", Endpoint{Serve: handleHome})

	mux.Handle("/", r)

	http.Handle("/static/", http.FileServer(http.Dir(*staticDir)))
	http.Handle("/", r)

	if *prof != "" {
		conf := &profile.Config{
			CPUProfile:  true,
			MemProfile:  true,
			ProfilePath: *prof,
		}
		defer profile.Start(conf).Stop()
	}

	log.Infof("Listening at %v...", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

type WireJSON struct {
	Command   string `json:"command"`
	Timestamp int64  `json:"timestamp"`
	DateStr   string `json:"dateStr"`

	// For generic messages
	Message string `json:"message"`

	// For blockchain sync
	Sync *SyncJSON `json:"sync"`

	// For "tx" message
	Tx *TxJSON `json:"tx"`

	// For "block" message
	Block *BlockJSON `json:"block"`

	// For "inv" message
	Inv []*InvJSON `json:"inv"`

	// For "addr" message
	Addresses []*AddrJSON `json:"addresses"`

	// For "block" and "blockheader" messages
	// Header []*BlockHeaderJSON `json:"header"`
}

type SyncJSON struct {
	Hash      string `json:"hash"`
	Height    int    `json:"height"`
	Tx        int    `json:"tx"`
	Timestamp int64  `json:"timestamp"`
	DateStr   string `json:"dateStr"`
}

type OutPointJSON struct {
	Hash  string `json:"hash"`
	Index int    `json:"index"`
}

type TxInJSON struct {
	PrevOutPoint *OutPointJSON `json:"prevOutPoint"`
	Value        uint64        `json:"value"`
	Type         string        `json:"type"`
	Address      string        `json:"address"`
	ScriptSig    string        `json:"scriptSig"`
}

type TxOutJSON struct {
	Value   uint64 `json:"value"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Script  string `json:"script"`
}

type TxJSON struct {
	Hash    string       `json:"hash"`
	Inputs  []*TxInJSON  `json:"inputs"`
	Outputs []*TxOutJSON `json:"outputs"`

	InputsValue  uint64 `json:"inputsValue"`
	OutputsValue uint64 `json:"outputsValue"`
	Fee          uint64 `json:"fee"`
	Bytes        int    `json:"bytes"`
}

type InvJSON struct {
	Type string `json:"type"`
	Hash string `json:"hash"`
}

type AddrJSON struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type BlockJSON struct {
	Hash            string `json:"hash"`
	Height          int    `json:"height"`
	Bytes           int    `json:"bytes"`
	NumTransactions int    `json:"numTransactions"`
}

type BlockHeaderJSON struct {
	Hash string `json:"hash"`
}

var msgChans = make(map[string]chan *WireJSON)
var msgChansLock sync.Mutex

func handleWireStream(w http.ResponseWriter, r *http.Request, ctx *Context) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	msgChansLock.Lock()
	id := uuid.New()
	ch := make(chan *WireJSON, 10)
	msgChans[id] = ch
	defer func() {
		msgChansLock.Lock()
		delete(msgChans, id)
		close(ch)
		conn.Close()
		msgChansLock.Unlock()
	}()
	msgChansLock.Unlock()

	for {
		msgJSON := <-ch

		data, err := json.Marshal(msgJSON)
		if err != nil {
			log.Errorf("couldn't marshal JSON %v: %s", msgJSON, err)
			continue
		}

		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return fmt.Errorf("couldn't write to websocket: %s", err)
		}
	}

	return nil
}

type InfoJSON struct {
	IP                  string  `json:"ip"`
	Port                int     `json:"port"`
	TestNet             bool    `json:"testNet"`              // getinfo "testnet"
	Version             int32   `json:"version"`              // getinfo "version"
	Height              int32   `json:"height"`               // getinfo "blocks"
	HeadersHeight       int32   `json:"headersHeight"`        // getchaintips, "headers-only" section
	Connections         int32   `json:"connections"`          // getinfo "connections"
	Difficulty          float64 `json:"difficulty"`           // getinfo "difficulty"
	HashesPerSec        int64   `json:"hashesPerSec"`         // getmininginfo "networkhashps"
	VerificationProgess float64 `json:"verificationProgress"` // getblockchaininfo "verificationprogress"
	BytesRecv           uint64  `json:"bytesRecv"`            // getnettotals "totalbytesrecv"
	BytesSent           uint64  `json:"bytesSent"`            // getnettotals "totalbytessent"
}

func handleInfoJSON(w http.ResponseWriter, r *http.Request, ctx *Context) error {
	infoJSON := latestInfoJSON
	data, err := json.Marshal(infoJSON)
	if err != nil {
		return fmt.Errorf("couldn't marshal JSON %v: %s", infoJSON, err)
	}

	w.Write(data)

	return nil
}

func handleInfoStream(w http.ResponseWriter, r *http.Request, ctx *Context) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	prevData := []byte("")
	for {
		time.Sleep(1000 * time.Millisecond)
		infoJSON := latestInfoJSON

		data, err := json.Marshal(infoJSON)
		if err != nil {
			log.Errorf("couldn't marshal JSON %v: %s", infoJSON, err)
			continue
		}

		if bytes.Equal(prevData, data) {
			continue
		}

		prevData = data

		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return fmt.Errorf("couldn't write to websocket: %s", err)
		}
	}

	return nil
}

func handleHome(w http.ResponseWriter, r *http.Request, ctx *Context) error {
	// TODO(ortutay): I cannot figure out why the commented code below is not
	// working as expected...
	// funcMap := template.FuncMap{}
	// tmpl := template.Must(template.New("/home/marcell/gocode/src/github.com/ortutay/nodez/templates/_base.html").
	// 	Funcs(funcMap).
	// 	ParseFiles(
	// 	"/home/marcell/gocode/src/github.com/ortutay/nodez/templates/_base.html",
	// 	"/home/marcell/gocode/src/github.com/ortutay/nodez/templates/nodez.html"))

	file, err := os.Open(fmt.Sprintf("%s/nodez.html", *templatesPath))
	if err != nil {
		return err
	}
	buf, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}

	tmpl := template.Must(template.New("nodez").Parse(string(buf)))
	tc := make(map[string]interface{})
	if err := tmpl.Execute(w, tc); err != nil {
		return err
	}

	return nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Infof("Error: %v\nfor request: %v\n", err, r)
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}

func isDevMode(r *http.Request) bool {
	return r.Host == fmt.Sprintf("localhost:%s", *port)
}

func displayAddressFromPkScript(script []byte) (string, error) {
	scriptClass, addresses, _, err := txscript.ExtractPkScriptAddrs(
		script, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("couldn't get addresses: %s", err)
	}
	if (scriptClass == txscript.PubKeyHashTy ||
		scriptClass == txscript.ScriptHashTy) && len(addresses) == 1 {
		return addresses[0].EncodeAddress(), nil
	}
	return "", nil
}

func displayTypeFromPkScript(script []byte) (string, error) {
	scriptClass, _, _, err := txscript.ExtractPkScriptAddrs(
		script, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("couldn't get script type: %s", err)
	}
	switch scriptClass {
	case txscript.NonStandardTy:
		return "non-standard", nil
	case txscript.PubKeyTy:
		return "p2pk", nil
	case txscript.PubKeyHashTy:
		return "p2pkh", nil
	case txscript.ScriptHashTy:
		return "p2sh", nil
	case txscript.MultiSigTy:
		return "multi-sig", nil
	case txscript.NullDataTy:
		return "data", nil
	default:
		return "", fmt.Errorf("unhandled script class: %d", scriptClass)
	}
}

func msgToJSON(msg wire.Message) (*WireJSON, error) {
	var wireMsg WireJSON

	now := time.Now()
	wireMsg.Timestamp = now.Unix()
	wireMsg.DateStr = now.Format(dateFormat)

	wireMsg.Command = msg.Command()
	switch msg := msg.(type) {
	case *wire.MsgInv:
		for _, inv := range msg.InvList {
			invMsg := InvJSON{}
			invMsg.Hash = inv.Hash.String()

			switch inv.Type {
			case wire.InvTypeError:
				log.Errorf("inv type error: %d", inv.Type)
				continue
			case wire.InvTypeTx:
				invMsg.Type = "tx"
			case wire.InvTypeBlock:
				invMsg.Type = "block"
			case wire.InvTypeFilteredBlock:
				invMsg.Type = "filteredBlock"
			default:
				return nil, fmt.Errorf("unknown inv type: %d", inv.Type)
			}

			wireMsg.Inv = append(wireMsg.Inv, &invMsg)
		}

	case *wire.MsgAddr:
		for _, addr := range msg.AddrList {
			addrJSON := &AddrJSON{
				IP:   addr.IP.String(),
				Port: int(addr.Port),
			}
			wireMsg.Addresses = append(wireMsg.Addresses, addrJSON)
		}

	case *wire.MsgBlock:
		hash, err := msg.Header.BlockSha()
		if err != nil {
			return nil, err
		}
		block := btcutil.NewBlock(msg)
		wireMsg.Block = &BlockJSON{
			Hash:            hash.String(),
			Height:          int(block.Height()),
			Bytes:           msg.SerializeSize(),
			NumTransactions: len(msg.Transactions),
		}

	case *wire.MsgTx:
		hash, err := msg.TxSha()
		if err != nil {
			return nil, err
		}
		wireMsg.Tx = &TxJSON{Hash: hash.String()}

		for _, txIn := range msg.TxIn {
			op := txIn.PreviousOutPoint
			prevTx, err := getTx(op.Hash.Bytes())
			if int(op.Index) >= len(prevTx.TxOut) {
				return nil, fmt.Errorf("expected outpoint %d for tx %s, got %v", op.Index, hash.String(), prevTx.TxOut)
			}
			prevOut := prevTx.TxOut[op.Index]

			addressStr, err := displayAddressFromPkScript(prevOut.PkScript)
			if err != nil {
				return nil, err
			}

			typeStr, err := displayTypeFromPkScript(prevOut.PkScript)
			if err != nil {
				return nil, err
			}

			txInJSON := TxInJSON{
				PrevOutPoint: &OutPointJSON{
					Hash:  op.Hash.String(),
					Index: int(op.Index),
				},
				Value:     uint64(prevOut.Value),
				Type:      typeStr,
				Address:   addressStr,
				ScriptSig: hex.EncodeToString(txIn.SignatureScript),
			}

			wireMsg.Tx.Inputs = append(wireMsg.Tx.Inputs, &txInJSON)
			wireMsg.Tx.InputsValue += txInJSON.Value
		}

		for _, txOut := range msg.TxOut {
			addressStr, err := displayAddressFromPkScript(txOut.PkScript)
			if err != nil {
				return nil, err
			}

			typeStr, err := displayTypeFromPkScript(txOut.PkScript)
			if err != nil {
				return nil, err
			}

			txOutJSON := TxOutJSON{
				Value:   uint64(txOut.Value),
				Type:    typeStr,
				Address: addressStr,
			}

			wireMsg.Tx.Outputs = append(wireMsg.Tx.Outputs, &txOutJSON)
			wireMsg.Tx.OutputsValue += txOutJSON.Value
		}

		wireMsg.Tx.Fee = wireMsg.Tx.InputsValue - wireMsg.Tx.OutputsValue
		wireMsg.Tx.Bytes = msg.SerializeSize()

	case *wire.MsgPing:
		wireMsg.Message = strconv.Itoa(int(msg.Nonce))
	case *wire.MsgPong:
		wireMsg.Message = strconv.Itoa(int(msg.Nonce))

	default:
		wireMsg.Message = ""
	}

	return &wireMsg, nil
}

func writeToChannels(wireJSON *WireJSON) {
	msgChansLock.Lock()
	for _, ch := range msgChans {
		ch <- wireJSON
	}
	msgChansLock.Unlock()
}

func fakeBitcoinStream() {
	tx1Hash, err := wire.NewShaHashFromStr("752140443f73bc6ed58623d28c82393682f1895ea8ce8aec53ca00f847342a50")
	if err != nil {
		log.Fatal(err)
	}

	tx2Hash, err := wire.NewShaHashFromStr("d58623d28c82393682f1895ea8ce8aec53ca00f847342a50752140443f73bc6e")
	if err != nil {
		log.Fatal(err)
	}

	_, err = wire.NewShaHashFromStr("93682f1895ea8ce8aec53ca00f847342a50752140443f73bc6ed58623d28c823")
	if err != nil {
		log.Fatal(err)
	}

	address1, err := btcutil.DecodeAddress("12gpXQVcCL2qhTNQgyLVdCFG2Qs2px98nV", &chaincfg.MainNetParams)
	if err != nil {
		log.Fatal(err)
	}

	script1, err := txscript.PayToAddrScript(address1)
	if err != nil {
		log.Fatal(err)
	}

	op1 := wire.OutPoint{*tx1Hash, 0}
	op2 := wire.OutPoint{*tx2Hash, 1}

	hex1, err := hex.DecodeString("1234567890abcdef")
	if err != nil {
		log.Fatal(err)
	}
	hex2, err := hex.DecodeString("fedcba0987654321")
	if err != nil {
		log.Fatal(err)
	}

	txIn1 := wire.NewTxIn(&op1, hex1)
	txIn2 := wire.NewTxIn(&op2, hex2)

	txOut1 := wire.NewTxOut(1000, script1)

	msgTx1 := wire.NewMsgTx()
	msgTx1.AddTxIn(txIn1)
	msgTx1.AddTxIn(txIn2)
	msgTx1.AddTxOut(txOut1)

	log.Infof("msg tx 1: %v", msgTx1)

	prevBlockHash, err := wire.NewShaHashFromStr("000000008c82393682f1895ea8ce8aec53ca00f847342a50752140443f73bc6e")
	if err != nil {
		log.Fatal(err)
	}

	merkleRootHash, err := wire.NewShaHashFromStr("40443f73bc6ed58623d28c82393682f1895ea8ce8aec53ca00f847342a507521")
	if err != nil {
		log.Fatal(err)
	}

	header := wire.NewBlockHeader(prevBlockHash, merkleRootHash, 32, 1)
	msgBlock := wire.NewMsgBlock(header)
	msgBlock.AddTransaction(msgTx1)

	log.Infof("block: %v", msgBlock)

	for {
		time.Sleep(2000 * time.Millisecond)

		// msgInv := wire.MsgInv{
		// 	InvList: []*wire.InvVect{
		// 		&wire.InvVect{wire.InvTypeTx, *tx1Hash},
		// 		&wire.InvVect{wire.InvTypeTx, *tx2Hash},
		// 	},
		// }

		txJSON, err := msgToJSON(msgTx1)
		if err != nil {
			log.Fatal(err)
		}
		writeToChannels(txJSON)

		blockJSON, err := msgToJSON(msgBlock)
		if err != nil {
			log.Fatal(err)
		}
		writeToChannels(blockJSON)
	}
}

func bitcoinStream(pver uint32, btcnet wire.BitcoinNet) {
	// Try to connect to local bitcoin node
	var conn net.Conn
	var errJSON *WireJSON

	for {
		if conn != nil {
			conn.Close()
			conn = nil
		}

		if errJSON != nil {
			writeToChannels(errJSON)
			time.Sleep(1 * time.Second)
			errJSON = nil
		}

		var err error
		conn, err = net.Dial("tcp", *nodeAddr)
		if err != nil {
			log.Errorf("bitcoin stream dialing %s: %s", *nodeAddr, err)
			errJSON = &WireJSON{Command: "error", Message: "bitcoin node unreachable"}
			continue
		}

		// Send version message
		verMsg, err := wire.NewMsgVersionFromConn(conn, 1, 0)
		if err != nil {
			log.Errorf("bitcoin node version gave: %s", err)
			errJSON = &WireJSON{Command: "error", Message: "bitcoin node error"}
			continue
		}
		if err := wire.WriteMessage(conn, verMsg, pver, btcnet); err != nil {
			log.Errorf("bitcoin node write gave: %s", err)
			errJSON = &WireJSON{Command: "error", Message: "bitcoin node error"}
			continue
		}
		_, _, err = wire.ReadMessage(conn, pver, btcnet)
		if err != nil {
			log.Errorf("bitcoin node read gave: %s", err)
			errJSON = &WireJSON{Command: "error", Message: "bitcoin node error"}
			continue
		}

		for {
			msg, _, err := wire.ReadMessage(conn, pver, btcnet)
			if err != nil {
				log.Errorf("bitcoin node read gave: %s", err)
				errJSON = &WireJSON{Command: "error", Message: "bitcoin node error"}
				break
			}

			msgJSON, err := msgToJSON(msg)
			if err != nil {
				log.Errorf("couldn't convert %v to JSON: %s", msg, err)
				errJSON = &WireJSON{Command: "error", Message: "serialization error"}
				break
			}
			writeToChannels(msgJSON)
		}
	}
}

func bitcoindDebugLogStream(debugLog string) {
	log.Infof("Streaming %v", debugLog)
	for {
		cmd := exec.Command("tail", "-f", debugLog)

		out, err := cmd.StdoutPipe()
		if err != nil {
			log.Fatal("Couldn't get stdout for bitcoind debug log: %s", err)
		}

		if err := cmd.Start(); err != nil {
			log.Fatal("Couldn't stream bitcoind debug log: %s", err)
		}

		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			// We only need the debug log to stream sync output
			if !isSyncing {
				time.Sleep(5 * time.Second)
				break
			}

			t := scanner.Text()
			m := bitcoindUpdateTipRE.FindStringSubmatch(t)

			if m != nil {
				bestHash, heightStr, txStr, dateStr := m[1], m[2], m[3], m[4]

				height, err := strconv.ParseInt(heightStr, 10, 64)
				if err != nil {
					log.Errorf("Couldn't parse height %s: %s", heightStr, err)
				}

				tx, err := strconv.ParseInt(txStr, 10, 64)
				if err != nil {
					log.Errorf("Couldn't parse tx %s: %s", txStr, err)
				}

				date, err := time.Parse(bitcoindDateLayout, dateStr)
				if err != nil {
					log.Errorf("Couldn't parse date %s: %s", dateStr, err)
				}

				now := time.Now()
				wireJSON := &WireJSON{
					Command: "sync",
					Timestamp: now.Unix(),
					DateStr: now.Format(dateFormat),
					Sync: &SyncJSON{
						Hash:      bestHash,
						Height:    int(height),
						Tx:        int(tx),
						Timestamp: date.Unix(),
						DateStr:   date.Format(dateFormat),
					},
				}
				// log.Infof("%v %v %v %v", bestHash, height, tx, date)
				writeToChannels(wireJSON)
			}
		}
	}
}

func nodeInfo() (*InfoJSON, error) {
	var infoJSON InfoJSON

	infoJSON.IP = myIP.String()
	s := strings.Split(*nodeAddr, ":")
	port, err := strconv.ParseInt(s[1], 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	infoJSON.Port = int(port)

	info, err := rpcClient.GetInfo()
	if err != nil {
		return nil, fmt.Errorf("getinfo: %s", err)
	}

	miningInfo, err := rpcClient.GetMiningInfo()
	if err != nil {
		return nil, fmt.Errorf("getmininginfo: %s", err)
	}

	// TODO(ortutay): add this to btcrpclient
	// blockChainInfo, err := rpcClient.GetBlockChainInfo()
	// if err != nil {
	// 	return nil, fmt.Errorf("getblockchaininfo: %s", err)
	// }

	netTotalsInfo, err := rpcClient.GetNetTotals()
	if err != nil {
		return nil, fmt.Errorf("getnettotals: %s", err)
	}

	infoJSON.TestNet = info.TestNet
	infoJSON.Version = info.Version
	infoJSON.Height = info.Blocks
	infoJSON.Connections = info.Connections
	infoJSON.Difficulty = info.Difficulty

	infoJSON.HashesPerSec = miningInfo.NetworkHashPS

	// infoJSON.VerificationProgess = blockChainInfo.VerificationProgess

	infoJSON.BytesRecv = netTotalsInfo.TotalBytesRecv
	infoJSON.BytesSent = netTotalsInfo.TotalBytesSent

	rawTips, err := getChainTips()
	if err != nil {
		log.Error(err)
	} else {
		var tips []chainTipJSON
		if err := json.Unmarshal(rawTips, &tips); err != nil {
			return nil, fmt.Errorf("couldn't unmarshal %s: %s", string(rawTips), err)
		}
		for _, tip := range tips {
			switch tip.Status {
			case "headers-only", "valid-headers":
				if tip.Height > infoJSON.HeadersHeight {
					infoJSON.HeadersHeight = tip.Height
				}
			}
		}
	}

	isSyncing = infoJSON.Height != 0 && infoJSON.HeadersHeight != 0 && infoJSON.HeadersHeight-infoJSON.Height > 5

	return &infoJSON, nil
}

type chainTipJSON struct {
	Height    int32  `json:"height"`
	Hash      string `json:"string"`
	BranchLen int32  `json:"branchlen"`
	Status    string `json:"status"`
}

var errNoBitcoinCLI = errors.New("no bitcoin-cli available")

func getChainTips() ([]byte, error) {
	if *bitcoinCLI == "" {
		return nil, errNoBitcoinCLI
	}

	var args []string
	if *testnet {
		args = append(args, "-testnet")
	}
	args = append(args, "-conf=" + *bitcoinConf)
	args = append(args, "getchaintips")
	cmd := exec.Command(*bitcoinCLI, args...)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("couldn't get output %s %v: %s", *bitcoinCLI, args, err)
	}

	return out, nil
}

func updateNodeInfo() {
	for {
		infoJSON, err := nodeInfo()
		if err != nil {
			log.Error(err)
			time.Sleep(1 * time.Second)
			continue
		}

		latestInfoJSON = *infoJSON
		time.Sleep(2000 * time.Millisecond)
	}
}

func getTx(txHash []byte) (*wire.MsgTx, error) {
	// TODO
	tx1Hash, err := wire.NewShaHashFromStr("752140443f73bc6ed58623d28c82393682f1895ea8ce8aec53ca00f847342a50")
	if err != nil {
		log.Fatal(err)
	}

	tx2Hash, err := wire.NewShaHashFromStr("d58623d28c82393682f1895ea8ce8aec53ca00f847342a50752140443f73bc6e")
	if err != nil {
		log.Fatal(err)
	}

	address1, err := btcutil.DecodeAddress("12gpXQVcCL2qhTNQgyLVdCFG2Qs2px98nV", &chaincfg.MainNetParams)
	if err != nil {
		log.Fatal(err)
	}

	script1, err := txscript.PayToAddrScript(address1)
	if err != nil {
		log.Fatal(err)
	}

	op1 := wire.OutPoint{*tx1Hash, 1}
	op2 := wire.OutPoint{*tx2Hash, 2}

	hex1, err := hex.DecodeString("1234567890abcdef")
	if err != nil {
		log.Fatal(err)
	}
	hex2, err := hex.DecodeString("fedcba0987654321")
	if err != nil {
		log.Fatal(err)
	}

	txIn1 := wire.NewTxIn(&op1, hex1)
	txIn2 := wire.NewTxIn(&op2, hex2)

	txOut1 := wire.NewTxOut(1000, script1)
	txOut2 := wire.NewTxOut(2000, script1)

	msgTx1 := wire.NewMsgTx()
	msgTx1.AddTxIn(txIn1)
	msgTx1.AddTxIn(txIn2)
	msgTx1.AddTxOut(txOut1)
	msgTx1.AddTxOut(txOut2)

	return msgTx1, nil
}
