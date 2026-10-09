package linux

import "testing"

// realUbuntuDomainXML is the metadata element virt-manager writes for an Ubuntu
// 22.04 guest, embedded in the surrounding `virsh dumpxml` document the way the
// collector actually receives it.
const realUbuntuDomainXML = `<domain type='kvm'>
  <name>web-01</name>
  <uuid>1d2c3b4a-0000-1111-2222-333344445555</uuid>
  <metadata>
    <libosinfo:libosinfo xmlns:libosinfo="http://libosinfo.org/xmlns/libvirt/domain/1.0">
      <libosinfo:os id="http://ubuntu.com/ubuntu/22.04"/>
    </libosinfo:libosinfo>
  </metadata>
  <memory unit='KiB'>4194304</memory>
</domain>`

func TestDomainMetadataOS(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "virt-manager ubuntu domain",
			xml:  realUbuntuDomainXML,
			want: "Ubuntu 22.04",
		},
		{
			name: "windows client",
			xml:  `<metadata><libosinfo:os id="http://microsoft.com/win/10"/></metadata>`,
			want: "Windows 10",
		},
		{
			name: "windows server shorthand year",
			xml:  `<metadata><libosinfo:os id="http://microsoft.com/win/2k19"/></metadata>`,
			want: "Windows Server 2019",
		},
		{
			name: "windows server R2 shorthand year",
			xml:  `<metadata><libosinfo:os id="http://microsoft.com/win/2k12r2"/></metadata>`,
			want: "Windows Server 2012 R2",
		},
		{
			name: "rhel",
			xml:  `<metadata><libosinfo:os id="http://redhat.com/rhel/9.0"/></metadata>`,
			want: "Red Hat Enterprise Linux 9.0",
		},
		{
			name: "product without a version",
			xml:  `<metadata><libosinfo:os id="http://archlinux.org/archlinux"/></metadata>`,
			want: "Arch Linux",
		},
		{
			name: "unknown product still renders readably",
			xml:  `<metadata><libosinfo:os id="http://example.org/some-os/3"/></metadata>`,
			want: "Some Os 3",
		},
		{
			name: "id attribute is not the first attribute",
			xml:  `<metadata><libosinfo:os foo="bar" id="http://debian.org/debian/12"/></metadata>`,
			want: "Debian 12",
		},
		{
			name: "metadata present but no libosinfo",
			xml:  `<domain><metadata><myapp:custom xmlns:myapp="http://example.org/ns"/></metadata></domain>`,
			want: "",
		},
		{
			name: "domain without metadata",
			xml:  `<domain><name>bare</name></domain>`,
			want: "",
		},
		{
			name: "empty input",
			xml:  "",
			want: "",
		},
		{
			name: "os element without an id",
			xml:  `<metadata><libosinfo:os/></metadata>`,
			want: "",
		},
		{
			name: "attribute whose name ends in id is not the id",
			xml:  `<metadata><libosinfo:os invalid="nope" id="http://fedoraproject.org/fedora/38"/></metadata>`,
			want: "Fedora 38",
		},
		{
			// The id comes from the domain XML, so §10 normalization applies.
			name: "control characters and padding in the id are normalized away",
			xml:  "<metadata><libosinfo:os id=\"http://example.org/bad\x07-os/  \t2 \"/></metadata>",
			want: "Bad Os 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domainMetadataOS(tt.xml); got != tt.want {
				t.Errorf("domainMetadataOS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLibosinfoDisplayNameRejectsNonIDs(t *testing.T) {
	for _, id := range []string{"", "   ", "ubuntu", "http://ubuntu.com", "http://ubuntu.com/", ":///"} {
		if got := libosinfoDisplayName(id); got != "" {
			t.Errorf("libosinfoDisplayName(%q) = %q, want \"\"", id, got)
		}
	}
}

// TestLibosinfoOSIDSkipsEmptyElement guards the loop in libosinfoOSID: the first
// <libosinfo:os> element may carry no id, and the second one still must be found.
func TestLibosinfoOSIDSkipsEmptyElement(t *testing.T) {
	xml := `<metadata><libosinfo:os/><libosinfo:os id="http://centos.org/centos/7.0"/></metadata>`
	if got, want := libosinfoOSID(xml), "http://centos.org/centos/7.0"; got != want {
		t.Errorf("libosinfoOSID() = %q, want %q", got, want)
	}
}
