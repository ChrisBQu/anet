// +build android

package anet

import (
	"bytes"
	"errors"
	"net"
	"os"
	"syscall"
	"unsafe"
)

const (
	android11ApiLevel = 30
)

var (
	customAndroidApiLevel       = -1
	errInvalidInterface         = errors.New("invalid network interface")
	errInvalidInterfaceIndex    = errors.New("invalid network interface index")
	errInvalidInterfaceName     = errors.New("invalid network interface name")
	errNoSuchInterface          = errors.New("no such network interface")
	errNoSuchMulticastInterface = errors.New("no such multicast network interface")
)

type ifReq [40]byte

// Interfaces returns a list of the system's network interfaces.
func Interfaces() ([]net.Interface, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.Interfaces()
	}

	ift, err := interfaceTable(0)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	if len(ift) != 0 {
		zoneCache.update(ift, true)
		zoneCacheX.update(ift, true)
	}
	return ift, nil
}

// InterfaceAddrs returns a list of the system's unicast interface
// addresses.
//
// The returned list does not identify the associated interface; use
// Interfaces and Interface.Addrs for more detail.
func InterfaceAddrs() ([]net.Addr, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.InterfaceAddrs()
	}

	ifat, err := interfaceAddrTable(nil)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifat, err
}

// InterfaceByIndex returns the interface specified by index.
//
// On Solaris, it returns one of the logical network interfaces
// sharing the logical data link; for more precision use
// InterfaceByName.
func InterfaceByIndex(index int) (*net.Interface, error) {
	if androidApiLevel() < android11ApiLevel {
		return net.InterfaceByIndex(index)
	}

	if index <= 0 {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterfaceIndex}
	}
	ift, err := interfaceTable(index)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	ifi, err := interfaceByIndex(ift, index)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifi, err
}

// InterfaceByName returns the interface specified by name.
func InterfaceByName(name string) (*net.Interface, error) {
	if name == "" {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterfaceName}
	}
	ift, err := interfaceTable(0)
	if err != nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	if len(ift) != 0 {
		zoneCache.update(ift, true)
		zoneCacheX.update(ift, true)
	}
	for _, ifi := range ift {
		if name == ifi.Name {
			return &ifi, nil
		}
	}
	return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errNoSuchInterface}
}

// InterfaceAddrsByInterface returns a list of the system's unicast
// interface addresses by specific interface.
func InterfaceAddrsByInterface(ifi *net.Interface) ([]net.Addr, error) {
	if ifi == nil {
		return nil, &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: errInvalidInterface}
	}

	if androidApiLevel() < android11ApiLevel {
		return ifi.Addrs()
	}

	ifat, err := interfaceAddrTable(ifi)
	if err != nil {
		err = &net.OpError{Op: "route", Net: "ip+net", Source: nil, Addr: nil, Err: err}
	}
	return ifat, err
}

// SetAndroidVersion set the Android environment in which the program runs.
// The Android system version number can be obtained through
// `android.os.Build.VERSION.RELEASE` of the Android framework.
// If version is 0 the actual version will be detected automatically if possible.
func SetAndroidVersion(version uint) {
	switch {
	case version == 0:
		customAndroidApiLevel = -1
	case version >= 11:
		customAndroidApiLevel = android11ApiLevel
	default:
		customAndroidApiLevel = 0
	}
}

func androidApiLevel() int {
	if customAndroidApiLevel != -1 {
		// user-provided api level should be used
		return customAndroidApiLevel
	}

	// try to autodetect api level
	return androidDeviceApiLevel()
}

// ipv6ZoneCache stub for Android: no-op
type ipv6ZoneCache struct{}

func (zc *ipv6ZoneCache) update(_ []net.Interface, _ bool) bool { return false }

// these override the real linknames on Android
var zoneCache = &ipv6ZoneCache{}
var zoneCacheX = &ipv6ZoneCache{}

// If the ifindex is zero, interfaceTable returns mappings of all
// network interfaces. Otherwise it returns a mapping of a specific
// interface.
func interfaceTable(ifindex int) ([]net.Interface, error) {
	tab, err := NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return nil, os.NewSyscallError("netlinkrib", err)
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		return nil, os.NewSyscallError("parsenetlinkmessage", err)
	}

	var ift []net.Interface
	im := make(map[uint32]struct{})
loop:
	for _, m := range msgs {
		switch m.Header.Type {
		case syscall.NLMSG_DONE:
			break loop
		case syscall.RTM_NEWADDR:
			ifam := (*syscall.IfAddrmsg)(unsafe.Pointer(&m.Data[0]))
			if _, ok := im[ifam.Index]; ok {
				continue
			} else {
				im[ifam.Index] = struct{}{}
			}

			if ifindex == 0 || ifindex == int(ifam.Index) {
				ifi := newLink(ifam)
				if ifi != nil {
					ift = append(ift, *ifi)
				}
				if ifindex == int(ifam.Index) {
					break loop
				}
			}
		}
	}

	return ift, nil
}

func newLink(ifam *syscall.IfAddrmsg) *net.Interface {
	ift := &net.Interface{Index: int(ifam.Index)}

	name, err := indexToName(ifam.Index)
	if err != nil {
		return nil
	}
	ift.Name = name

	mtu, err := nameToMTU(name)
	if err != nil {
		return nil
	}
	ift.MTU = mtu

	flags, err := nameToFlags(name)
	if err != nil {
		return nil
	}
	ift.Flags = flags
	return ift
}

func linkFlags(rawFlags uint32) net.Flags {
	var f net.Flags
	if rawFlags&syscall.IFF_UP != 0 {
		f |= net.FlagUp
	}
	if rawFlags&syscall.IFF_RUNNING != 0 {
		f |= net.FlagRunning
	}
	if rawFlags&syscall.IFF_BROADCAST != 0 {
		f |= net.FlagBroadcast
	}
	if rawFlags&syscall.IFF_LOOPBACK != 0 {
		f |= net.FlagLoopback
	}
	if rawFlags&syscall.IFF_POINTOPOINT != 0 {
		f |= net.FlagPointToPoint
	}
	if rawFlags&syscall.IFF_MULTICAST != 0 {
		f |= net.FlagMulticast
	}
	return f
}

// If the ifi is nil, interfaceAddrTable returns addresses for all
// network interfaces. Otherwise it returns addresses for a specific
// interface.
func interfaceAddrTable(ifi *net.Interface) ([]net.Addr, error) {
	tab, err := NetlinkRIB(syscall.RTM_GETADDR, syscall.AF_UNSPEC)
	if err != nil {
		return nil, os.NewSyscallError("netlinkrib", err)
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		return nil, os.NewSyscallError("parsenetlinkmessage", err)
	}

	var ift []net.Interface
	if ifi == nil {
		var err error
		ift, err = interfaceTable(0)
		if err != nil {
			return nil, err
		}
	}
	ifat, err := addrTable(ift, ifi, msgs)
	if err != nil {
		return nil, err
	}
	return ifat, nil
}

func addrTable(ift []net.Interface, ifi *net.Interface, msgs []syscall.NetlinkMessage) ([]net.Addr, error) {
	var ifat []net.Addr
loop:
	for _, m := range msgs {
		switch m.Header.Type {
		case syscall.NLMSG_DONE:
			break loop
		case syscall.RTM_NEWADDR:
			ifam := (*syscall.IfAddrmsg)(unsafe.Pointer(&m.Data[0]))
			if len(ift) != 0 || ifi.Index == int(ifam.Index) {
				attrs, err := syscall.ParseNetlinkRouteAttr(&m)
				if err != nil {
					return nil, os.NewSyscallError("parsenetlinkrouteattr", err)
				}
				ifa := newAddr(ifam, attrs)
				if ifa != nil {
					ifat = append(ifat, ifa)
				}
			}
		}
	}
	return ifat, nil
}

func newAddr(ifam *syscall.IfAddrmsg, attrs []syscall.NetlinkRouteAttr) net.Addr {
	var ipPointToPoint bool
	// Seems like we need to make sure whether the IP interface
	// stack consists of IP point-to-point numbered or unnumbered
	// addressing.
	for _, a := range attrs {
		if a.Attr.Type == syscall.IFA_LOCAL {
			ipPointToPoint = true
			break
		}
	}
	for _, a := range attrs {
		if ipPointToPoint && a.Attr.Type == syscall.IFA_ADDRESS {
			continue
		}
		switch ifam.Family {
		case syscall.AF_INET:
			return &net.IPNet{IP: net.IPv4(a.Value[0], a.Value[1], a.Value[2], a.Value[3]), Mask: net.CIDRMask(int(ifam.Prefixlen), 8*net.IPv4len)}
		case syscall.AF_INET6:
			ifa := &net.IPNet{IP: make(net.IP, net.IPv6len), Mask: net.CIDRMask(int(ifam.Prefixlen), 8*net.IPv6len)}
			copy(ifa.IP, a.Value[:])
			return ifa
		}
	}
	return nil
}

func interfaceByIndex(ift []net.Interface, index int) (*net.Interface, error) {
	for _, ifi := range ift {
		if index == ifi.Index {
			return &ifi, nil
		}
	}
	return nil, errNoSuchInterface
}

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, e1 := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg))
	if e1 != 0 {
		return e1
	}
	return nil
}

func indexToName(index uint32) (string, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer syscall.Close(fd)

	var ifr ifReq
	*(*uint32)(unsafe.Pointer(&ifr[syscall.IFNAMSIZ])) = index
	err = ioctl(fd, syscall.SIOCGIFNAME, unsafe.Pointer(&ifr[0]))
	if err != nil {
		return "", err
	}

	return string(bytes.Trim(ifr[:syscall.IFNAMSIZ], "\x00")), nil
}

func nameToMTU(name string) (int, error) {
	// Leave room for terminating NULL byte.
	if len(name) >= syscall.IFNAMSIZ {
		return -1, syscall.EINVAL
	}

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	defer syscall.Close(fd)

	var ifr ifReq
	copy(ifr[:], name)
	err = ioctl(fd, syscall.SIOCGIFMTU, unsafe.Pointer(&ifr[0]))
	if err != nil {
		return -1, err
	}

	return int(*(*int32)(unsafe.Pointer(&ifr[syscall.IFNAMSIZ]))), nil
}

func nameToFlags(name string) (net.Flags, error) {
	// Leave room for terminating NULL byte.
	if len(name) >= syscall.IFNAMSIZ {
		return 0, syscall.EINVAL
	}

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd)

	var ifr ifReq
	copy(ifr[:], name)
	err = ioctl(fd, syscall.SIOCGIFFLAGS, unsafe.Pointer(&ifr[0]))
	if err != nil {
		return 0, err
	}

	return linkFlags(*(*uint32)(unsafe.Pointer(&ifr[syscall.IFNAMSIZ]))), nil
}
