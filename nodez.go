package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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

	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcrpcclient"
	log "github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var (
	port        = flag.String("port", "8080", "Port to listen on")
	nodeAddr    = flag.String("node_addr", ":8333", "Bitcoin node address")
	bitcoinConf = flag.String("bitcoin_conf", "~/.bitcoin/bitcoin.conf", "Bitcoin configuration file")
	debugLog    = flag.String("debug_log", "~/.bitcoin/debug.log", "bitcoind debug log")
	staticDir   = flag.String("static_dir", ".", "Path to static files")
	testnet     = flag.Bool("testnet", false, "Connect to testnet")

	bitcoindUpdateTipRE = regexp.MustCompile(
		`UpdateTip:.*best=([0-9a-f]+).*height=(\d+).*tx=(\d+).*date=(.*) progress`)
	bitcoindDateLayout = "2006-01-02 15:04:05"

	rpcClient *btcrpcclient.Client

	latestInfoJSON InfoJSON

	myIP net.IP
)

func main() {
	flag.Parse()

	resp, err := http.Get("http://myexternalip.com/raw")
	if err != nil {
		log.Fatal(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	myIP = net.ParseIP(strings.TrimSpace(string(body)))
	log.Infof("My IP: %s", myIP.String())
	resp.Body.Close()

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

	var btcnet wire.BitcoinNet
	if *testnet {
		btcnet = wire.TestNet3
	} else {
		btcnet = wire.MainNet
	}

	go bitcoinStream(wire.ProtocolVersion, btcnet)
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

	log.Infof("Listening at %v...", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

type WireJSON struct {
	Command string `json:"command"`

	// For generic messages
	Message string `json:"message"`

	// For blockchain sync
	Sync *SyncJSON `json:"sync"`

	// For "inv" message
	Inv []*InvJSON `json:"inv"`

	// For "addr" message
	Addresses []*AddrJSON `json:"addresses"`

	// For "block" and "blockheader" messages
	Header []*BlockHeaderJSON `json:"header"`
}

type SyncJSON struct {
	Hash      string `json:"hash"`
	Height    int    `json:"height"`
	Tx        int    `json:"tx"`
	Timestamp int64  `json:"timestamp"`
}

type InvJSON struct {
	Type string `json:"type"`
	Hash string `json:"hash"`
}

type AddrJSON struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
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
	ch := make(chan *WireJSON)
	msgChans[id] = ch
	defer func() {
		delete(msgChans, id)
		close(ch)
		conn.Close()
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

	file, err := os.Open("templates/nodez.html")
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

func msgToJSON(msg wire.Message) (*WireJSON, error) {
	var wireMsg WireJSON
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
		wireMsg.Header = []*BlockHeaderJSON{&BlockHeaderJSON{Hash: hash.String()}}

	case *wire.MsgPing:
		wireMsg.Message = strconv.Itoa(int(msg.Nonce))
	case *wire.MsgPong:
		wireMsg.Message = strconv.Itoa(int(msg.Nonce))

	default:
		wireMsg.Message = ""
	}

	return &wireMsg, nil
}

func bitcoinStream(pver uint32, btcnet wire.BitcoinNet) {
	// Try to connect to local bitcoin node
	conn, err := net.Dial("tcp", *nodeAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// Send version message
	verMsg, err := wire.NewMsgVersionFromConn(conn, 1, 0)
	if err != nil {
		log.Fatal(err)
	}
	if err := wire.WriteMessage(conn, verMsg, pver, btcnet); err != nil {
		log.Fatal(err)
	}
	_, _, err = wire.ReadMessage(conn, pver, btcnet)
	if err != nil {
		log.Fatal(err)
	}

	for {
		msg, _, err := wire.ReadMessage(conn, pver, btcnet)
		if err != nil {
			log.Fatal(err)
		}

		msgJSON, err := msgToJSON(msg)
		if err != nil {
			log.Error(err)
			continue
		}
		for _, ch := range msgChans {
			ch <- msgJSON
		}
	}
}

func bitcoindDebugLogStream(debugLog string) {
	log.Infof("Streaming %v", debugLog)
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

			wireJSON := &WireJSON{
				Command: "sync",
				Sync: &SyncJSON{
					Hash:      bestHash,
					Height:    int(height),
					Tx:        int(tx),
					Timestamp: date.Unix(),
				},
			}
			// log.Infof("%v %v %v %v", bestHash, height, tx, date)

			for _, ch := range msgChans {
				ch <- wireJSON
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

	return &infoJSON, nil
}

func updateNodeInfo() {
	for {
		infoJSON, err := nodeInfo()
		if err != nil {
			log.Error(err)
		}
		latestInfoJSON = *infoJSON
		time.Sleep(1000 * time.Millisecond)
	}
}
