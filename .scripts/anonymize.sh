#!/bin/sh
# Rewrites what capture.sh printed so it can be shown to others, or kept as a test: every address and serial number is swapped for a made-up one,
# the same one each time it appears, and anything sas-exporter does not read is left out. Models, firmware versions, speeds and counts stay as they
# were.
#
#   .scripts/anonymize.sh < capture.txt > lib/sysfs/sysfstest/testdata/nas.txt
#
# An address keeps its first seven digits, which say who made the thing and not which one it is. In the pages an enclosure answered with, the
# name of each power supply is swapped too: the backplanes this was written for name a power supply by its serial number. The other names in
# those pages are left as they are, so look at them before posting a capture of an enclosure of another make.
set -eu

exec awk -F '\t' '
BEGIN {
	OFS = "\t"

	# the files sas-exporter reads, by name
	split("board_name version_fw version_bios host_sas_address ioc_reset_count state proc_name unique_id version current_link_speed current_link_width max_link_speed max_link_width phy_identifier negotiated_linkrate maximum_linkrate sas_address invalid_dword_count running_disparity_error_count loss_of_dword_sync_count phy_reset_problem_count vendor_id product_id product_rev vendor model rev type wwid iorequest_cnt iodone_cnt ioerr_cnt iotmo_cnt id status fault locate slot vpd_pg80", names, " ")
	for (i in names) read[names[i]] = 1

	for (i = 32; i < 127; i++) code[sprintf("%c", i)] = i
}

# address is the made-up address that stands for a real one.
function address(real) {
	if (!(real in addresses)) addresses[real] = substr(real, 1, 7) sprintf("%09x", ++made)

	return addresses[real]
}

# serial is a page holding a serial number, in hex, with the number swapped for a made-up one of the same length: the four bytes that say what the
# page is stay, and so does the padding around the number.
function serial(hex,    out, i, byte, first, last, fake, n) {
	out = substr(hex, 1, 8)
	first = 0

	for (i = 9; i < length(hex); i += 2) {
		byte = substr(hex, i, 2)
		if (byte != "20" && byte != "00") {
			if (!first) first = i
			last = i
		}
	}
	if (!first) return hex

	n = (last - first) / 2 + 1
	fake = sprintf("Z%0" (n - 1) "d", ++serials)

	for (i = 9; i < length(hex); i += 2) {
		if (i >= first && i <= last) out = out sprintf("%02x", code[substr(fake, (i - first) / 2 + 1, 1)])
		else out = out substr(hex, i, 2)
	}

	return out
}

# number is what a run of hex digits says.
function number(hex,    i, n) {
	n = 0
	for (i = 1; i <= length(hex); i++) n = n * 16 + index("0123456789abcdef", substr(hex, i, 1)) - 1

	return n
}

# byte is the byte that many bytes into a page, as a number.
function byte(hex, offset) {
	return number(substr(hex, 2 * offset + 1, 2))
}

# kinds notes what an enclosure lists in the page that describes it: which kind of thing each group is and how many it holds, in the order the
# other pages go through them.
function kinds(device, hex,    i, start) {
	groups[device] = byte(hex, 10)
	start = 12 + byte(hex, 11)

	for (i = 0; i < groups[device]; i++) {
		kind[device, i] = byte(hex, start + 4 * i)
		held[device, i] = byte(hex, start + 4 * i + 1)
	}
}

# supply is the made-up name, in hex, that stands for the real name of a power supply: as long as the real one, with what pads it left as it was.
function supply(real,    n, i, fake, out) {
	if (real in supplies) return supplies[real]

	n = length(real) / 2
	while (n > 0 && (substr(real, 2 * n - 1, 2) == "00" || substr(real, 2 * n - 1, 2) == "20")) n--
	if (n < 3) return real

	fake = sprintf("PS%0" (n - 2) "d", ++supplied)
	for (i = 1; i <= n; i++) out = out sprintf("%02x", code[substr(fake, i, 1)])
	supplies[real] = out substr(real, 2 * n + 1)

	return supplies[real]
}

# named is the page an enclosure names its things in, with each power supply under a made-up name. Every group has one name for the group and
# then one for each thing in it, each after four bytes of which the last two say how long it is.
function named(device, hex,    at, i, j, len) {
	at = 8

	for (i = 0; i < groups[device]; i++) {
		for (j = 0; j <= held[device, i]; j++) {
			len = byte(hex, at + 2) * 256 + byte(hex, at + 3)
			if (kind[device, i] == 2 && j > 0 && len > 0) hex = substr(hex, 1, 2 * (at + 4)) supply(substr(hex, 2 * (at + 4) + 1, 2 * len)) substr(hex, 2 * (at + 4 + len) + 1)

			at += 4 + len
		}
	}

	return hex
}

/^#/ { next }

{
	name = $2
	sub(/.*\//, "", name)
}

$1 == "d" || $1 == "l" { print; next }

# a file that was only named is kept when it is the one every directory has, so a test still sees a directory with more in it than is read
$1 == "-" { if (name == "uevent") print; next }

# a page an enclosure answered with holds the address of the enclosure, which by now has a made-up one to stand for it, and one of them the
# names of its power supplies. The page that names things cannot be read without the one that lists them, and is left out when that is missing.
$1 == "s" {
	split($2, asked, "/")
	if (asked[2] == "01") kinds(asked[1], $3)
	if (asked[2] == "07" && !(asked[1] in groups)) next
	if (asked[2] == "07") $3 = named(asked[1], $3)

	for (real in addresses) gsub(real, addresses[real], $3)
	print
	next
}

!(name in read) { next }

$1 == "!" { sub(/.*: /, "", $3); print; next }

$1 == "x" { $3 = serial($3); print; next }

$1 == "f" && $3 ~ /^0x[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]$/ { $3 = "0x" address(substr($3, 3)); print; next }

$1 == "f" && name == "wwid" && $3 ~ /^naa\./ { $3 = "naa." address(substr($3, 5)); print; next }

$1 == "f" { print }
'
