package main

import (
	"bufio"
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
)

func main() {
	flag.Parse()

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

	client, err := btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Shutdown()

	// Get the current block count.
	// TODO(ortutay): if error, probably syncing
	blockCount, err := client.GetBlockCount()
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

	r := mux.NewRouter()
	mux := http.NewServeMux()

	r.Handle("/nodez/wire/stream", Endpoint{Serve: handleWireStream})
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
	Command string

	// For blockchain sync
	Sync *SyncJSON

	// For "inv" message
	Inv []InvJSON `json:",omitempty"`
}

type SyncJSON struct {
	Hash      string
	Height    int
	Tx        int
	Timestamp int64
}

type InvJSON struct {
	Type string
	Hash string
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
			log.Errorf("couldn't write to websocket: %s", err)
			continue
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

			wireMsg.Inv = append(wireMsg.Inv, invMsg)
		}
	}
	return &wireMsg, nil
}

func bitcoinStream(pver uint32, btcnet wire.BitcoinNet) {
	// Try to connect to local bitcoin node
	conn, err := net.Dial("tcp", *nodeAddr)
	if err != nil {
		log.Fatal(err)
	}

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
			log.Infof("%v %v %v %v", bestHash, height, tx, date)

			for _, ch := range msgChans {
				ch <- wireJSON
			}
		}
	}
}
