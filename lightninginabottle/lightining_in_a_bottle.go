package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"os/exec"
	"strings"
	"time"

	// "os/user"
	// "path"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"gopkg.in/macaroon.v2"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// IDEA: Incorporate this functionality into Bitstream for LND setup, but consider making this
// a standalone repository for setting up containerized lightning network development environments
// As a bonus, generalize this to set up a configurable number of lightning nodes

// TODO: Construct a Makefile with target that starts containers and then runs this code.
// Then user will be free to start Bitstream server and client

// Currently the peer connection is persisted across restarts of the containers (probably volumes)
// With this program we don't really need to persist any data across restarts

// TODO: Pass TLS and Macaroon Config to EnableLightning. Since this program calls
// EnableLightning for both the Bitstream Client and Server it will need to handle different
// authentication configurations

const bitstreamRPCPort = "10009"
const clientRPCPort = "10003"
const serverTLSCertPath = "../config/server/lnd/tls.cert"
const serverMacaroonPath = "../config/server/macaroons/admin.macaroon"
const clientTLSCertPath = "../config/client/lnd/tls.cert"
const clientMacaroonPath = "../config/client/macaroons/admin.macaroon"

func main() {
	log.Println("Setting up Bitstream Lightning Network development environment ...")
	ctx := context.Background()

	// TODO: TLS and Macaroon for LND authentication

	// Setup Bitstream Server Lightning Network Node
	log.Println("Enabling Lightning to Server")
	bitstreamServerLnd, err := EnableLightning(bitstreamRPCPort, serverTLSCertPath, serverMacaroonPath)
	if err != nil {
		log.Fatalf("unable to connect to lnd on behalf of Bitstream server: %v", err)
	}
	log.Println("Complete")

	serverInfo, err := bitstreamServerLnd.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		log.Fatalf("unable to get info for server: %v", err)
	}
	log.Printf("Bitstream Info: %+v", serverInfo)

	// Setup Bitstream Client Lightning Network Node
	bitstreamClientLnd, err := EnableLightning(clientRPCPort, clientTLSCertPath, clientMacaroonPath)
	if err != nil {
		log.Fatalf("unable to connect to lnd on behalf of Bitstream client: %v", err)
	}

	clientInfo, err := bitstreamClientLnd.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		log.Fatalf("unable to get info for client: %v", err)
	}
	log.Printf("client Info: %+v", clientInfo)

	// Create Wallets
	serverAddr, _ := bitstreamServerLnd.NewAddress(ctx, &lnrpc.NewAddressRequest{})
	log.Printf("Bitstream Server address: %+v", serverAddr.Address)

	clientAddr, _ := bitstreamClientLnd.NewAddress(ctx, &lnrpc.NewAddressRequest{})
	log.Printf("Bitstream Client address: %+v", clientAddr.Address)

	// Fund Client Wallet
	cmdOutput, err := execCommand("./mine.sh", "400", clientAddr.Address)
	if err != nil {
		log.Printf("unable to run mine.sh: %v", err)
	}
	log.Println("Script Output: ", cmdOutput)
	time.Sleep(30 * time.Second)

	// Connect Peer
	log.Println("Attempting to connect to peer ...")
	clientIP, err := execCommand("./container_ip.sh")
	if err != nil {
		log.Printf("unable to run container_ip.sh: %v", err)
	}
	clientIP = strings.TrimRight(clientIP, "\n")
	log.Println("Bitstream client container IP: ", clientIP)

	peerAddress := &lnrpc.LightningAddress{Pubkey: clientInfo.IdentityPubkey, Host: clientIP + ":9735"} //TODO: Obtain Host IP from Docker dynamically - docker inspect bitstream-client | grep IP
	log.Printf("Peer Address: %+v", peerAddress)

	connectPeerResp, err := bitstreamServerLnd.ConnectPeer(ctx, &lnrpc.ConnectPeerRequest{Addr: peerAddress})
	if err != nil {
		if !strings.Contains(err.Error(), "already connected to peer") {
			log.Fatalf("unable to connect to peer: %+v", err)
		}
	}
	log.Printf("Connect Peer Response: %+v", connectPeerResp)

	// Open Channel
	publicBytes, _ := hex.DecodeString(serverInfo.IdentityPubkey)
	log.Printf("Attempting to open channel with public key: %x", publicBytes)
	channel, err := bitstreamClientLnd.OpenChannel(ctx, &lnrpc.OpenChannelRequest{
		NodePubkeyString: serverInfo.IdentityPubkey,
		NodePubkey:       publicBytes,
		// PushSat:            40000,
		LocalFundingAmount: 5000000,
	})
	if err != nil {
		log.Printf("unable to open channel with peer: %+v", err)
	}

	time.Sleep(15 * time.Second)
	// Confirm the funding transaction with sufficient depth
	cmdOutput, err = execCommand("./mine.sh", "6", clientAddr.Address)
	if err != nil {
		log.Printf("unable to run mine.sh: %v", err)
	}
	log.Println("Script Output: ", cmdOutput)

	time.Sleep(30 * time.Second)
	for i := 0; i < 10; i++ {
		log.Printf("Verifying successful channel open ...")
		statusUpdate, err := channel.Recv()
		if err != nil {
			log.Printf("encountered error attempting to read from channel update status stream: %+v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if _, ok := statusUpdate.Update.(*lnrpc.OpenStatusUpdate_ChanOpen); ok {
			log.Println("Channel from client to Bitstream server opened successfully!")
			log.Println("Bitstream Lightning Network development environment is operational!")
			break
		}

		time.Sleep(2 * time.Second)
	}
}

// EnableLightning opens a gRPC connection to lightning network daemon backend
func EnableLightning(rpcPort, tlsCertPath, macaroonPath string) (lnrpc.LightningClient, error) {

	tlsCreds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
	if err != nil {
		return nil, fmt.Errorf("Cannot get node tls credentials: %v", err)
	}

	macaroonBytes, err := ioutil.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("Cannot read macaroon file: %v", err)
	}

	mac := &macaroon.Macaroon{}
	if err = mac.UnmarshalBinary(macaroonBytes); err != nil {
		return nil, fmt.Errorf("Cannot unmarshal macaroon: %v", err)
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithBlock(),
		grpc.WithPerRPCCredentials(macaroons.NewMacaroonCredential(mac)),
	}

	conn, err := grpc.Dial("localhost:"+rpcPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot dial lnd backend: %v", err)
	}

	lightningClient := lnrpc.NewLightningClient(conn)
	return lightningClient, nil
}

func execCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)

	log.Println("COMMAND: ", cmd.Dir, cmd.Args)

	outBytes, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(outBytes), nil
}
