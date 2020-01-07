package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path"
	"time"

	"projects/bitstream/rpc"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Bitstream Client
var (
	dataWindow      time.Duration = 15 * time.Second
	pauseTimeout    time.Duration = 5 * time.Second
	bitstreamClient BitstreamClient
)

// BitstreamClient represents a client of the BitstreamServer
// which streams payments via Lightning Network in exchange for random data (for now!)
type BitstreamClient struct {
	LightningClient lnrpc.LightningClient
	ID              string
	// BytesStreamConn *net.TCPConn
	// BitstreamInvoiceStream *grpc.ClientConn
}

func init() {
	// Initialize the Bitstream Payment Streamer
	lightningClient, err := EnableLightning()
	if err != nil {
		log.Fatalf("[PaymentStreamer init]: unable to establish connection to lnd RPC server: %v", err)
	}

	bitstreamClient = BitstreamClient{
		LightningClient: lightningClient,
	}
}

func main() {

	// Stream random bytes from Bitstream server using raw TCP connection
	tcpAddr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Fatal(errors.Wrap(err, "unable to resolve TCP address"))
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		log.Fatal(errors.Wrap(err, "unable to establish TCP connection to server"))
	}
	defer conn.Close()

	// Connects to our (Bitstream) gRPC server.
	gRPCConn, err := grpc.Dial(":8081", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("unable to establish connection to :8081: %v", err)
	} else {
		log.Println("connection established")
	}
	defer conn.Close()

	// Stream Lightning Network payment requests from Bitstream server (gRPC)
	// and send a stream of payments via Bitstream client's lightning node
	log.Println("Connected to Bitstream using local address: ", conn.LocalAddr())
	bitstreamClient.ID = conn.LocalAddr().String()
	go bitstreamClient.streamPayment(gRPCConn)

	// Stream data from Bitstream server
	streamRandomData(conn)

	// NOTE: We might need to add a way to communicate between the main go routine streaming random
	// bytes and the go routine streaming payment so that we can discontinue payment should we
	// stop receiving data. This seems like Flow Control. Is this neccessary??
}

func streamRandomData(conn net.Conn) {
	for {
		// conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var buf [128]byte
		n, err := conn.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				log.Println("[inside streamRandomData]: The server has ended the stream. Closing connection to Bitstream server")
			} else {
				log.Printf("[inside streamRandomData]: Finished with error: %v", err)
			}
			return
		}
		os.Stdout.Write(buf[:n])

		// If last piece of data was recieved more than X seconds ago,
		// pause the payment stream
		// if time.Since(lastReceivedData()) > dataWindow {
		// 	continueStream <- false
		// }
	}
}

func (bitstream *BitstreamClient) streamPayment(conn *grpc.ClientConn) {
	// continueStream := make(chan bool)
	// SendPayment RPC https://github.com/lightningnetwork/lnd/tree/master/lnrpc
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Generates a lightning invoice client to stream payment requests from
	// Bitstream server (with all the functionality we expect as defined in the protobuf)
	client := rpc.NewPaymentsClient(conn)

	// Stream Payment requests from Bitstream server
	stream, err := client.StreamPaymentRequests(ctx, &rpc.StreamPaymentRequest{
		ClientID: bitstream.ID,
	})
	if err != nil {
		log.Fatalf("[inside streamPayment]: unable to obtain stream: %v", err)
	}

	// Stream payment to Bitstream server
	for {
		// Await receipt of payment request from Bitstream server
		paymentRequest, err := stream.Recv()
		if err == io.EOF {
			// cancel()
			break
		}
		if err != nil {
			log.Fatalf("%v.StreamPaymentRequests(_) = _, %v", client, err)
			break
		}
		// log.Println("[inside streamPayment]: Payment request received! ", paymentRequest)

		// Send Lightning Payment to Bitstream server
		// TODO: Investigate use of SendPayment RPC for bidirectional payment streams
		_, payErr := bitstream.LightningClient.SendPaymentSync(context.Background(), &lnrpc.SendRequest{
			PaymentRequest: paymentRequest.PaymentRequest,
		})
		if payErr != nil {
			log.Println("[inside streamPayment]: Unable to complete payment!!! ", payErr)
		}
		// select {
		// case <-continueStream:
		// }
	}
}

// EnableLightning opens a gRPC connection to lightning network daemon backend
func EnableLightning() (lnrpc.LightningClient, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("Cannot get current user: %v", err)
	}
	// fmt.Println("The user home directory: " + usr.HomeDir)
	tlsCertPath := path.Join(usr.HomeDir, "Library/Application Support/Lnd/tls.cert")
	// macaroonPath := path.Join(usr.HomeDir, ".lnd/admin.macaroon")

	tlsCreds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
	if err != nil {
		return nil, fmt.Errorf("Cannot get node tls credentials: %v", err)
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithBlock(),
		// grpc.WithPerRPCCredentials(macaroons.NewMacaroonCredential(mac)),
	}

	conn, err := grpc.Dial("localhost:10003", opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot dial lnd backend: %v", err)
	}

	client := lnrpc.NewLightningClient(conn)
	return client, nil
}

// func lastReceivedData() time.Time {
// 	return time.Now()
// }
