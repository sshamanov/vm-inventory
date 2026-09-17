package linux

import (
	"strings"

	"vm-inventory/internal/shared"
)

// domainMetadataOS returns the guest operating system recorded in libvirt domain
// metadata, which is the second source in the precedence order of
// ARCHITECTURE.md §10.2. virt-manager and virt-install record a libosinfo OS
// identifier there:
//
//	<metadata>
//	  <libosinfo:libosinfo xmlns:libosinfo="http://libosinfo.org/xmlns/libvirt/domain/1.0">
//	    <libosinfo:os id="http://ubuntu.com/ubuntu/22.04"/>
//	  </libosinfo:libosinfo>
//	</metadata>
//
// Domains created without OS info carry no such element, and the result is "" —
// the caller then treats the guest OS as unavailable rather than guessing one.
// The id is host-supplied text, so the name derived from it is normalized to the
// §10 rules before it can become a metric label.
func domainMetadataOS(xml string) string {
	return shared.CleanDescription(libosinfoDisplayName(libosinfoOSID(xml)))
}

// libosinfoOSID extracts the id attribute of the libosinfo <os> element from
// `virsh dumpxml` output. The element is namespace-prefixed and the id is not
// necessarily the first attribute, so the element is located first and its
// attributes are only then searched.
func libosinfoOSID(xml string) string {
	const elem = "<libosinfo:os"
	for start := 0; ; {
		idx := strings.Index(xml[start:], elem)
		if idx < 0 {
			return ""
		}
		idx += start
		tag := xml[idx:]
		if end := strings.IndexByte(tag, '>'); end >= 0 {
			tag = tag[:end]
		}
		if id := attrValue(tag, "id"); id != "" {
			return id
		}
		start = idx + len(elem)
	}
}

// attrValue returns the value of a quoted XML attribute inside a single tag.
// The match must start at an attribute boundary, so that e.g. an `invalid`
// attribute is not mistaken for `id`.
func attrValue(tag, attr string) string {
	needle := attr + "="
	for start := 0; ; {
		idx := strings.Index(tag[start:], needle)
		if idx < 0 {
			return ""
		}
		idx += start
		if idx == 0 || tag[idx-1] == ' ' || tag[idx-1] == '\t' || tag[idx-1] == '\n' {
			rest := strings.TrimSpace(tag[idx+len(needle):])
			if len(rest) == 0 || rest[0] != '"' {
				return ""
			}
			rest = rest[1:]
			if end := strings.IndexByte(rest, '"'); end >= 0 {
				return rest[:end]
			}
			return ""
		}
		start = idx + len(needle)
	}
}

// libosinfoProducts maps the middle segment of a libosinfo OS id to the name
// people use for that operating system. The id is a declaration, not a source of
// truth about the running system; this only spells out what it already states.
var libosinfoProducts = map[string]string{
	"ubuntu":      "Ubuntu",
	"debian":      "Debian",
	"centos":      "CentOS",
	"rhel":        "Red Hat Enterprise Linux",
	"fedora":      "Fedora",
	"opensuse":    "openSUSE",
	"sles":        "SUSE Linux Enterprise Server",
	"sled":        "SUSE Linux Enterprise Desktop",
	"alpine":      "Alpine Linux",
	"ol":          "Oracle Linux",
	"oraclelinux": "Oracle Linux",
	"archlinux":   "Arch Linux",
	"gentoo":      "Gentoo",
	"openbsd":     "OpenBSD",
	"freebsd":     "FreeBSD",
	"netbsd":      "NetBSD",
	"rocky":       "Rocky Linux",
	"almalinux":   "AlmaLinux",
	"win":         "Windows",
}

// libosinfoDisplayName turns a libosinfo OS id such as
// "http://ubuntu.com/ubuntu/22.04" into "Ubuntu 22.04". Anything that does not
// look like an id, or whose product segment is missing, yields "".
func libosinfoDisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}

	// Format: scheme://host/product[/version], e.g. http://ubuntu.com/ubuntu/22.04
	slash := strings.Index(id, "://")
	if slash < 0 {
		return ""
	}
	rest := id[slash+3:]
	firstSlash := strings.IndexByte(rest, '/')
	if firstSlash < 0 {
		return "" // host only, no product segment
	}
	parts := strings.Split(strings.Trim(rest[firstSlash+1:], "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}

	product, ok := libosinfoProducts[strings.ToLower(parts[0])]
	if !ok {
		product = titleCaseToken(parts[0])
	}

	if len(parts) < 2 || parts[1] == "" {
		return product
	}
	version := strings.ToLower(parts[1])

	// Windows ids use "win" for the client family and encode server releases as
	// 2k19/2k22 — the released name of that release is 2019/2022.
	if strings.EqualFold(parts[0], "win") && strings.HasPrefix(version, "2k") {
		product = "Windows Server"
	}
	version = expandWindowsVersion(version)
	return product + " " + version
}

// expandWindowsVersion rewrites the shorthand libosinfo uses for Windows years:
// "2k19" is 2019, "2k12r2" is "2012 R2".
func expandWindowsVersion(v string) string {
	if !strings.HasPrefix(v, "2k") {
		return v
	}
	if v[len(v)-2:] == "r2" {
		return "20" + v[2:len(v)-2] + " R2"
	}
	return "20" + v[2:]
}

// titleCaseToken renders an unrecognized product segment as a readable name.
func titleCaseToken(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
