def rule($chain;$args): "-A "+$chain+" "+$args;
$lan[0] as $l | $policy[0] as $f |
"*filter", ":MICROSTICK_INPUT - [0:0]", ":MICROSTICK_FORWARD - [0:0]",
(if $input_jump then "-I INPUT 1 -j MICROSTICK_INPUT" else empty end),
(if $forward_jump then "-I FORWARD 1 -j MICROSTICK_FORWARD" else empty end),
rule("MICROSTICK_INPUT";"-i lo -j ACCEPT"),
rule("MICROSTICK_INPUT";"-m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"),
rule("MICROSTICK_INPUT";"-i "+$usb+" -j ACCEPT"),
(if $mode=="ap" then rule("MICROSTICK_INPUT";"-i "+$wifi+" -j ACCEPT") else empty end),
(if $mode=="sta" then rule("MICROSTICK_INPUT";"-i "+$wifi+" -p tcp --dport 80 -j ACCEPT"),
 rule("MICROSTICK_INPUT";"-i "+$wifi+" -p udp --sport 67 --dport 68 -j ACCEPT"),
 (if $sta!="" then rule("MICROSTICK_INPUT";"-i "+$wifi+" -s "+$sta+" -p udp --dport 5353 -j ACCEPT") else empty end),
 (($f.services // [])[] | select(.enabled) | . as $r |
  (if $r.source!="" then $r.source else $sta end) as $source | select($source!="") |
  (if $r.protocol=="both" then ["tcp","udp"] else [$r.protocol] end)[] as $p |
  rule("MICROSTICK_INPUT";"-i "+$wifi+" -s "+$source+" -p "+$p+" --dport "+($r.port|split("-")|join(":"))+" -j ACCEPT"))
 else empty end),
(if $lte!="" then rule("MICROSTICK_INPUT";"-i "+$lte+" -p udp --sport 67 --dport 68 -j ACCEPT") else empty end),
rule("MICROSTICK_INPUT";"-j DROP"),
(if $up!="" then
 $f.rules[] | select(.enabled) | . as $r |
 ["usb",(if $mode=="ap" then "ap" else empty end)][] as $key |
 select($r.network=="all" or $r.network==$key) |
 (if $key=="usb" then $usb else $wifi end) as $iface |
 (if $r.protocol=="both" then ["tcp","udp"] elif $r.protocol=="any" then [""] else [$r.protocol] end)[] as $p |
 (if $p!="" then " -p "+$p else "" end) as $proto |
 (if $r.port!="" then " --dport "+($r.port|split("-")|join(":")) else "" end) as $dport |
 (if $r.port!="" then " --sport "+($r.port|split("-")|join(":")) else "" end) as $sport |
 rule("MICROSTICK_FORWARD";"-i "+$iface+" -o "+$up+$proto+$dport+
   (if $r.mac!="" then " -m mac --mac-source "+$r.mac else "" end)+
   (if $r.destination!="" then " -d "+$r.destination else "" end)+" -j DROP"),
 (if $r.mac=="" then [$l[$key].address] else [$clients[0][]|select(.network==$key and .mac==$r.mac)|.ips[]]|unique end)[] as $ip |
 rule("MICROSTICK_FORWARD";"-i "+$up+" -o "+$iface+" -d "+$ip+$proto+$sport+
   (if $r.destination!="" then " -s "+$r.destination else "" end)+" -j DROP")
else empty end),
rule("MICROSTICK_FORWARD";"-m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"),
(if $up!="" then rule("MICROSTICK_FORWARD";"-i "+$usb+" -o "+$up+" -j ACCEPT"),
 (if $mode=="ap" then rule("MICROSTICK_FORWARD";"-i "+$wifi+" -o "+$up+" -j ACCEPT") else empty end) else empty end),
rule("MICROSTICK_FORWARD";"-j DROP"),"COMMIT",
"*nat",":MICROSTICK_NAT - [0:0]",
(if $nat_jump then "-I POSTROUTING 1 -j MICROSTICK_NAT" else empty end),
(if $up!="" then rule("MICROSTICK_NAT";"-o "+$up+" -j MASQUERADE") else empty end),"COMMIT",
(if $mss then "*mangle","-A FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu","COMMIT" else empty end)
