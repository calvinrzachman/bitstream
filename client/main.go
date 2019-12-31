package main

import (
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/pkg/errors"
)

// Bitstream Client
var (
	dataWindow   time.Duration = 15 * time.Second
	pauseTimeout time.Duration = 5 * time.Second
)

func main() {
	// ctx := context.Background()
	// ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	// defer cancel()

	tcpAddr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Fatal(errors.Wrap(err, "unable to resolve TCP address"))
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		log.Fatal(errors.Wrap(err, "unable to establish TCP connection to server"))
	}

	defer conn.Close() // IMPORTANT: Connections must be closed otherwise they will hang around in CLOSE_WAIT

	// go streamPayment()

	// Stream data from Bitstream server
	streamRandomData(conn)

}

func streamRandomData(conn net.Conn) {
	for {
		// conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var buf [128]byte
		n, err := conn.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				log.Println("\nThe stream has ended. Closing connection to Bitstream server")
			} else {
				log.Printf("Finished with error: %v", err)
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

// func lastReceivedData() time.Time {
// 	return time.Now()
// }

// // LightningInvoice is the Bitstream client's
// // representation of Lighning Network invoices
// type LightningInvoice struct {
// 	invoice lnrpc.Invoice
// }

// // InvoiceStreamer is a gRPC client stream which receives
// // a stream on Lightning Network payment invoices from the Bitsream server
// type InvoiceStreamer struct {
// 	Invoices grpc.ClientStream
// }

// func streamPayment() {
// 	continueStream := make(chan bool)
// 	// SendPayment RPC https://github.com/lightningnetwork/lnd/tree/master/lnrpc

// 	// Stream invoices from Bitstream server
// 	var invoice LightningInvoice
// 	go func() {
// 		for {
// 			// invoice, err := grpc.blah blah blah.Recv(&invoice)
// 		}
// 	}()

// 	// Send Lightning network payment to Bitstream server

// 	// Stream payment to Bitstream server
// 	for {

// 		select {
// 		case <-continueStream:
// 		}
// 	}
// }
