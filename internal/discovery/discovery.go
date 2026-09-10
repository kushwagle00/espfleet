// Package discovery enumerates serial candidates without opening serial ports.
package discovery

import (
	"go.bug.st/serial/enumerator"
	"sort"
)

type Port struct {
	Name         string `json:"name"`
	USB          bool   `json:"usb"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Product      string `json:"product,omitempty"`
}

func List() ([]Port, error) {
	// No active USB probe filters: discovery must not interfere with devices.
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]Port, 0, len(ports))
	for _, p := range ports {
		out = append(out, Port{p.Name, p.IsUSB, p.VID, p.PID, p.SerialNumber, p.Product})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
