//go:build linux && (amd64 || arm64 || riscv64)

package devices

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/katbyte/sas-exporter/lib/mpt"
)

// The mpt3sas driver's own way in for a request to a card: MPT3COMMAND, which is _IOWR('L', 20, struct mpt3_ioctl_command), a struct 72 bytes long
// where a pointer is eight.
const (
	cardCommand = 0xc0484c14
	// cardTimeout is how many seconds the card is given, which is the least the driver allows. A card that does not answer in that time is
	// reset by the driver, so this is never asked of a card twice in a row.
	cardTimeout = 10
	// cardReply is room for the card's reply, of which the first part is read.
	cardReply = 64
)

// cardRequest is the driver's mpt3_ioctl_command, field for field, with the request to the card where the driver expects it: straight after.
type cardRequest struct {
	card         uint32
	port         uint32
	maximumData  uint32
	timeout      uint32
	reply        unsafe.Pointer
	dataIn       unsafe.Pointer
	dataOut      unsafe.Pointer
	sense        unsafe.Pointer
	replySize    uint32
	dataInSize   uint32
	dataOutSize  uint32
	senseSize    uint32
	requestWords uint32
	request      [mpt.RequestSize]byte
}

// The layout the driver expects: the request to the card starts 68 bytes in, and all of it is read from there.
var (
	_ [68]byte = [unsafe.Offsetof(cardRequest{}.request)]byte{}
	_ [96]byte = [unsafe.Sizeof(cardRequest{})]byte{}
)

// send puts one request to a card through its driver and gives back the header of the page it is about. A page asked for is written into page.
func send(fd int, card uint32, request [mpt.RequestSize]byte, page []byte) (mpt.Header, error) {
	reply := make([]byte, cardReply)
	r := cardRequest{card: card, timeout: cardTimeout, reply: unsafe.Pointer(&reply[0]), replySize: cardReply, requestWords: mpt.RequestSize / 4, request: request} //nolint:gosec // G103: the driver's interface takes the reply by address
	if len(page) > 0 {
		r.dataIn, r.dataInSize = unsafe.Pointer(&page[0]), uint32(len(page)) //nolint:gosec // G115: a page is at most 255 words long
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), cardCommand, uintptr(unsafe.Pointer(&r))) //nolint:gosec // G103: the driver's interface takes the request by address
	runtime.KeepAlive(reply)
	runtime.KeepAlive(page)
	// this is what the driver says once it has given up waiting for the card, and by then it has reset it
	if errno == syscall.ENODATA {
		return mpt.Header{}, fmt.Errorf("the card did not answer in %d seconds, and its driver resets a card that does not: %w", cardTimeout, errno)
	}
	if errno != 0 {
		return mpt.Header{}, errno
	}

	return mpt.Reply(reply)
}

// CardPage is the page a card keeps its own temperature in, asked of the card through its driver, and nothing for a card that has no such page.
// id is the number the driver knows the card by. Only cards on the mpt3sas driver are asked: it is the only one this has been written against.
func (d Dir) CardPage(ctx context.Context, driver string, id uint32) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if driver != cardDriver {
		return nil, fmt.Errorf("asking a card on the %s driver for its temperature: %w", driver, errors.ErrUnsupported)
	}

	path := filepath.Join(string(d), cardDevice)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = syscall.Close(fd) }()

	// the card is first asked whether it has the page and how long it is, as the driver itself does
	header, err := send(fd, id, mpt.HeaderRequest(mpt.IOUnit7), nil)
	if errors.Is(err, mpt.ErrNoPage) || (err == nil && header.Bytes() == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asking card %d through %s which pages it has: %w", id, path, err)
	}

	page := make([]byte, header.Bytes())
	if _, err := send(fd, id, mpt.ReadRequest(header), page); err != nil {
		return nil, fmt.Errorf("asking card %d through %s for its temperature: %w", id, path, err)
	}

	return page, nil
}
