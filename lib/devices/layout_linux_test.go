//go:build linux && (amd64 || arm64 || riscv64)

package devices

import (
	"testing"
	"unsafe"

	"github.com/katbyte/sas-exporter/lib/mpt"
)

// Both requests are handed to the kernel as bytes, so where each field sits is the kernel's to say. The numbers here are what a C compiler gives
// for the kernel's own sg_io_hdr and mpt3_ioctl_command where a pointer is eight bytes, and for the two requests' own numbers.
func TestTheRequestsAreLaidOutAsTheKernelExpects(t *testing.T) {
	t.Parallel()

	var sg sgRequest
	var card cardRequest
	for _, field := range []struct {
		name     string
		at, want uintptr
	}{
		{"an enclosure's request", unsafe.Sizeof(sg), 88},
		{"interface_id", unsafe.Offsetof(sg.interfaceID), 0},
		{"dxfer_direction", unsafe.Offsetof(sg.direction), 4},
		{"cmd_len", unsafe.Offsetof(sg.commandLength), 8},
		{"mx_sb_len", unsafe.Offsetof(sg.senseMaximum), 9},
		{"iovec_count", unsafe.Offsetof(sg.vectors), 10},
		{"dxfer_len", unsafe.Offsetof(sg.dataLength), 12},
		{"dxferp", unsafe.Offsetof(sg.data), 16},
		{"cmdp", unsafe.Offsetof(sg.command), 24},
		{"sbp", unsafe.Offsetof(sg.sense), 32},
		{"timeout", unsafe.Offsetof(sg.timeout), 40},
		{"flags", unsafe.Offsetof(sg.flags), 44},
		{"pack_id", unsafe.Offsetof(sg.packID), 48},
		{"usr_ptr", unsafe.Offsetof(sg.user), 56},
		{"status", unsafe.Offsetof(sg.status), 64},
		{"masked_status", unsafe.Offsetof(sg.maskedStatus), 65},
		{"msg_status", unsafe.Offsetof(sg.messageStatus), 66},
		{"sb_len_wr", unsafe.Offsetof(sg.senseWritten), 67},
		{"host_status", unsafe.Offsetof(sg.hostStatus), 68},
		{"driver_status", unsafe.Offsetof(sg.driverStatus), 70},
		{"resid", unsafe.Offsetof(sg.residual), 72},
		{"duration", unsafe.Offsetof(sg.duration), 76},
		{"info", unsafe.Offsetof(sg.info), 80},

		// the driver's own struct is 72 bytes, of which the last four are where the request to the card begins
		{"a card's request", unsafe.Sizeof(card), 68 + mpt.RequestSize},
		{"ioc_number", unsafe.Offsetof(card.card), 0},
		{"port_number", unsafe.Offsetof(card.port), 4},
		{"max_data_size", unsafe.Offsetof(card.maximumData), 8},
		{"timeout", unsafe.Offsetof(card.timeout), 12},
		{"reply_frame_buf_ptr", unsafe.Offsetof(card.reply), 16},
		{"data_in_buf_ptr", unsafe.Offsetof(card.dataIn), 24},
		{"data_out_buf_ptr", unsafe.Offsetof(card.dataOut), 32},
		{"sense_data_ptr", unsafe.Offsetof(card.sense), 40},
		{"max_reply_bytes", unsafe.Offsetof(card.replySize), 48},
		{"data_in_size", unsafe.Offsetof(card.dataInSize), 52},
		{"data_out_size", unsafe.Offsetof(card.dataOutSize), 56},
		{"max_sense_bytes", unsafe.Offsetof(card.senseSize), 60},
		{"data_sge_offset", unsafe.Offsetof(card.requestWords), 64},
		{"mf", unsafe.Offsetof(card.request), 68},
	} {
		if field.at != field.want {
			t.Errorf("%s is at %d, where the kernel has it at %d", field.name, field.at, field.want)
		}
	}

	// SG_IO, SG_DXFER_FROM_DEV and MPT3COMMAND
	if sgIO != 0x2285 || sgFromDevice != -3 || cardCommand != 0xc0484c14 {
		t.Errorf("the requests are numbered %#x, %d and %#x, where the kernel has 0x2285, -3 and 0xc0484c14", sgIO, sgFromDevice, uint32(cardCommand))
	}
}
