def octets: [(. / 16777216|floor)%256,(. / 65536|floor)%256,(. / 256|floor)%256,.%256]|map(tostring)|join(".");
def mask: split("/")[1]|tonumber|4294967296-pow(2;32-.)|octets;
. as $l |
"bind-dynamic", "user=root", "interface="+$usb, "interface=lo", "no-hosts", "domain-needed", "bogus-priv",
"resolv-file="+$run+"/upstream-resolv.conf", "dhcp-leasefile="+$run+"/dnsmasq.leases", "dhcp-lease-max=64",
"dhcp-hostsfile="+$run+"/dhcp-hosts",
(if $mode=="ap" then "interface="+$wifi else empty end),
(["usb",(if $mode=="ap" then "ap" else empty end)][] as $key |
 $l[$key] as $n | ($n.address|split("/")[0]) as $ip |
 if $n.enabled then
 "dhcp-range=set:"+$key+","+$n.start+","+$n.end+","+($n.address|mask)+","+($n.lease|tostring)+"s",
 "dhcp-option=tag:"+$key+",3,"+$ip,
 "dhcp-option=tag:"+$key+",6,"+(if ($n.dns|length)>0 then $n.dns|join(",") else $ip end)
 else empty end)
