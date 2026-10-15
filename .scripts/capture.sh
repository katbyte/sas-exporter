#!/bin/sh
# Writes down what the kernel says of this machine's SAS cards, as text: the name of everything under each card, and what the files sas-exporter reads
# hold. It only reads and leaves nothing behind. The output names disks by serial number and address, so look before posting it.
#
#   .scripts/capture.sh > capture.txt                      # on the machine with the cards
#   ssh nas 'sh -s' < .scripts/capture.sh > capture.txt    # or from another, with nothing copied over
#
# It needs no root for any of that. Run as root on a machine with sg_ses, it also writes down the pages each enclosure describes itself in,
# which is where the fans, power supplies and temperatures are.
#
# Each line is a kind, a path and for most kinds a value, with a tab between them. The path is under /sys, but for an enclosure's page:
#   d  a directory
#   l  a link, and where it points
#   f  a file sas-exporter reads, and its first line
#   x  the same for a file that is not text, as hex
#   !  a file sas-exporter reads that could not be read, and why
#   -  any other file, by name only: it is never opened
#   s  a page an enclosure answered with, as hex: the path is the device it was asked through and the page's number
set -u

sys="${1:-/sys}"

# the files that are opened, by name: the ones sas-exporter reads and no others
read_names=' board_name version_fw version_bios host_sas_address ioc_reset_count state proc_name unique_id phy_identifier negotiated_linkrate maximum_linkrate sas_address invalid_dword_count running_disparity_error_count loss_of_dword_sync_count phy_reset_problem_count vendor_id product_id product_rev vendor model rev type wwid iorequest_cnt iodone_cnt ioerr_cnt iotmo_cnt id status fault locate slot host_busy can_queue board_tracer board_assembly version_product version_nvdata_persistent version_nvdata_default version_mpi minimum_linkrate maximum_linkrate_hw minimum_linkrate_hw component_vendor_id component_id component_revision_id queue_depth timeout power_status '
hex_names=' vpd_pg80 inquiry '
# the card's own link to the machine, which the kernel keeps beside the card
slot_names=' current_link_speed current_link_width max_link_speed max_link_width '

# entry writes the line for one path: what it is, and what it holds when it is a file sas-exporter reads.
entry() {
	path=$1
	names=$2
	rel=${path#"$sys"/}
	name=${path##*/}

	if [ -L "$path" ]; then
		printf 'l\t%s\t%s\n' "$rel" "$(readlink "$path")"
		return
	fi

	if [ -d "$path" ]; then
		printf 'd\t%s\n' "$rel"
		return
	fi

	case "$names" in
	*" $name "*)
		if value=$(head -n 1 "$path" 2>&1); then
			printf 'f\t%s\t%s\n' "$rel" "$value"
		else
			printf '!\t%s\t%s\n' "$rel" "$value"
		fi
		return
		;;
	esac

	case "$hex_names" in
	*" $name "*)
		if value=$(od -An -v -tx1 "$path" 2>&1); then
			printf 'x\t%s\t%s\n' "$rel" "$(printf '%s' "$value" | tr -d ' \n')"
		else
			printf '!\t%s\t%s\n' "$rel" "$value"
		fi
		return
		;;
	esac

	printf -- '-\t%s\n' "$rel"
}

found=0
for class in "$sys"/class/sas_host/host*; do
	[ -e "$class" ] || continue
	found=1

	# the card's own directory, which everything hanging off it is under
	root=$(cd "$class/device" && pwd -P) || continue
	host=${root##*/}

	entry "$class" ''
	entry "$sys/class/scsi_host/$host" ''

	for name in $slot_names; do
		[ -e "${root%/*}/$name" ] && entry "${root%/*}/$name" "$slot_names"
	done

	# the driver's version is kept apart from the card
	if driver=$(head -n 1 "$root/scsi_host/$host/proc_name" 2>/dev/null) && [ -e "$sys/module/$driver/version" ]; then
		printf 'f\t%s\t%s\n' "module/$driver/version" "$(head -n 1 "$sys/module/$driver/version")"
	fi

	# never gone into: nothing sas-exporter reads is under these, and a disk's own directory is named and left there
	find "$root" \( -name power -o -name subsystem -o -name driver -o -name firmware_node -o -name bsg -o -name scsi_generic -o -name scsi_disk -o -name sas_end_device -o -name sas_port \) -prune -o \( -path '*/block/*' -prune -print \) -o -print | while IFS= read -r path; do
		entry "$path" "$read_names"
	done
done

# what an enclosure says of what is in it is not in /sys: it is asked for, which takes root and a tool to ask with. Only this machine's own can be
for class in "$sys"/class/enclosure/*; do
	[ -e "$class" ] || continue
	entry "$class" ''

	generic=$(readlink "$class/device/generic" 2>/dev/null) || continue
	device=${generic##*/}
	if [ "$sys" != /sys ] || [ ! -r "/dev/$device" ] || ! command -v sg_ses >/dev/null 2>&1; then
		echo "the pages of the enclosure ${class##*/} are left out: they take root, sg_ses and this machine's own /sys" >&2
		continue
	fi

	for page in 01 02 05 07 0a; do
		if hex=$(sg_ses --page="0x$page" -rr "/dev/$device" 2>/dev/null | od -An -v -tx1 | tr -d ' \n') && [ -n "$hex" ]; then
			printf 's\t%s\t%s\n' "$device/$page" "$hex"
		else
			echo "the enclosure ${class##*/} would not give page $page" >&2
		fi
	done
done

if [ "$found" -eq 0 ]; then
	echo "no SAS card under $sys/class/sas_host" >&2
	exit 1
fi
