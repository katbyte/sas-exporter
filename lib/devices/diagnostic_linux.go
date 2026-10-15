//go:build linux

package devices

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// The kernel's generic SCSI interface: one request to send a command and wait for its answer.
const (
	sgIO = 0x2285
	// sgInterface marks the request as being in the form used here, and sgFromDevice says the command brings data back.
	sgInterface  = 'S'
	sgFromDevice = -3
	// sgTimeout is how long an enclosure is given, in thousandths of a second: as long as the kernel's own enclosure driver gives one.
	sgTimeout = 30000
	senseSize = 32
)

// sgRequest is the kernel's sg_io_hdr, field for field.
type sgRequest struct {
	interfaceID   int32
	direction     int32
	commandLength uint8
	senseMaximum  uint8
	vectors       uint16
	dataLength    uint32
	data          unsafe.Pointer
	command       unsafe.Pointer
	sense         unsafe.Pointer
	timeout       uint32
	flags         uint32
	packID        int32
	user          unsafe.Pointer
	status        uint8
	maskedStatus  uint8
	messageStatus uint8
	senseWritten  uint8
	hostStatus    uint16
	driverStatus  uint16
	residual      int32
	duration      uint32
	info          uint32
}

// Diagnostic is one page of an enclosure's diagnostics, asked of the SCSI device of that name: sg26. The device is opened for reading only, and
// the kernel then lets nothing through it but commands that read.
func (d Dir) Diagnostic(ctx context.Context, device string, page byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !plain(device) {
		return nil, fmt.Errorf("%q is not the name of a device", device)
	}

	path := filepath.Join(string(d), device)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = syscall.Close(fd) }()

	command, sense, data := diagnosticCommand(page), make([]byte, senseSize), make([]byte, allocation)
	request := sgRequest{interfaceID: sgInterface, direction: sgFromDevice, commandLength: uint8(len(command)), senseMaximum: senseSize, dataLength: allocation, data: unsafe.Pointer(&data[0]), command: unsafe.Pointer(&command[0]), sense: unsafe.Pointer(&sense[0]), timeout: sgTimeout} //nolint:gosec // G103: the kernel's interface takes each of these by address

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), sgIO, uintptr(unsafe.Pointer(&request))) //nolint:gosec // G103: the kernel's interface takes the request by address
	runtime.KeepAlive(data)
	runtime.KeepAlive(sense)
	if errno != 0 {
		return nil, fmt.Errorf("asking %s for page %#02x: %w", path, page, errno)
	}

	if request.status != 0 || request.hostStatus != 0 || request.driverStatus&0x0f != 0 {
		return nil, fmt.Errorf("%s refused page %#02x: status %#02x, host %#02x, driver %#02x, sense % x", path, page, request.status, request.hostStatus, request.driverStatus, sense[:min(int(request.senseWritten), senseSize)])
	}

	return diagnosticPage(page, data[:max(allocation-int(request.residual), 0)])
}
