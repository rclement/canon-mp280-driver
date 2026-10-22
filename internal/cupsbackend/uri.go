// SPDX-License-Identifier: GPL-2.0-only

package cupsbackend

import (
	"fmt"
	"strings"
)

// Scheme is the device URI scheme of the wrapper backend ("mp280usb:").
const Scheme = "mp280usb"

// StockScheme is the scheme of CUPS' own usb backend.
const StockScheme = "usb"

// SwapScheme replaces the scheme of a device URI: "mp280usb://Canon/..."
// -> "usb://Canon/...".
func SwapScheme(uri, from, to string) (string, error) {
	rest, ok := strings.CutPrefix(uri, from+":")
	if !ok {
		return "", fmt.Errorf("unexpected device URI scheme (%s)", uri)
	}
	return to + ":" + rest, nil
}
