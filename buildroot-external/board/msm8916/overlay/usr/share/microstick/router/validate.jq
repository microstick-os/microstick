def need($ok;$field): if $ok then . else error($field) end;
def textok: type=="string" and utf8bytelength<=64 and all(explode[]; .>=32 and .!=127);
def digits: type=="string" and length>0 and all(explode[]; .>=48 and .<=57);
def ipnum:
 if type!="string" then error("address") else
 split(".") as $p |
 if ($p|length)!=4 or (all($p[]; digits and length<=3 and (length==1 or .[0:1]!="0"))|not) then error("address") else
 $p|map(tonumber)|if any(.[]; .>255) then error("address") else reduce .[] as $n (0; .*256+$n) end end end;
def cidr:
 split("/") as $s | need(($s|length)==2;"address") |
 ($s[0]|ipnum) as $ip | ($s[1]|tonumber) as $p |
 need($p>=8 and $p<=30 and $p==($p|floor);"address") |
 pow(2;32-$p) as $b | {ip:$ip,prefix:$p,base:(($ip/$b|floor)*$b),size:$b};
def private: (.base>=167772160 and .base+.size<=184549376) or (.base>=2886729728 and .base+.size<=2887778304) or (.base>=3232235520 and .base+.size<=3232301056);
def inside($c): .>$c.base and .<($c.base+$c.size-1);
def macok:
 type=="string" and (ascii_downcase as $m | ($m|split(":")) as $p |
 ($p|length)==6 and all($p[]; length==2 and all(explode[]; (.>=48 and .<=57) or (.>=97 and .<=102))) and
 ("02468ace"|contains($m[1:2])) and $m!="00:00:00:00:00:00");
def network($key):
 . as $n | ($n.address|cidr) as $c |
 need(($c|private) and ($c.ip|inside($c));$key+".address") |
 need(($n.enabled|type)=="boolean";$key+".enabled") |
 ($n.start|ipnum) as $a | ($n.end|ipnum) as $b |
 need(($a|inside($c)) and ($b|inside($c)) and $a<=$b and ($c.ip<$a or $c.ip>$b);$key+".range") |
 need(($n.lease|type)=="number" and $n.lease>=120 and $n.lease<=604800 and $n.lease==($n.lease|floor);$key+".lease") |
 need(($n.dns|type)=="array" and ($n.dns|length)<=2;$key+".dns") |
 need(all($n.dns[]; (ipnum as $v | $v>0 and $v<3758096384 and ($v<2130706432 or $v>=2147483648)));$key+".dns") |
 {address:$n.address,enabled:$n.enabled,start:$n.start,end:$n.end,lease:$n.lease,dns:$n.dns};
def lan:
 . as $raw | {usb:(.usb|network("usb")),ap:(.ap|network("ap")),reservations:.reservations} |
 . as $l | (.usb.address|cidr) as $u | (.ap.address|cidr) as $a |
 need(($u.base+$u.size<=$a.base or $a.base+$a.size<=$u.base);"overlap") |
 need((.reservations|type)=="array" and (.reservations|length)<=64;"reservations") |
 .reservations |= map(. as $r |
   need(.network=="usb" or .network=="ap";"reservation.network") |
   need((.mac|macok);"reservation.mac") | need((.name|textok);"reservation.name") |
   ($l[.network].address|cidr) as $c |
   need(.ip=="" or (.ip|ipnum|inside($c) and .!=$c.ip);"reservation.ip") |
   {network,mac:(.mac|ascii_downcase),ip,name}) |
 need((.reservations|map(.network+":"+.mac)|unique|length)==(.reservations|length);"duplicate_mac") |
 need((.reservations|map(select(.ip!="")|.ip)|unique|length)==(.reservations|map(select(.ip!=""))|length);"duplicate_ip");
def firewall:
 need((.rules|type)=="array" and (.rules|length)<=64;"rules") |
 {rules:[.rules[] |
   need((.name|textok) and (.enabled|type)=="boolean";"rule.name") |
   need(.network=="all" or .network=="usb" or .network=="ap";"rule.network") |
   need(.mac=="" or (.mac|macok);"rule.mac") |
   need(.mac=="" or .network!="all";"rule.network") |
   need(.protocol=="any" or .protocol=="tcp" or .protocol=="udp" or .protocol=="both";"rule.protocol") |
   need((.destination|type)=="string";"rule.destination") |
   (if .destination!="" then (.destination|split("/")) as $d | ($d[0]|ipnum) as $v |
      need(($d|length)<=2 and (if ($d|length)==2 then ($d[1]|tonumber) as $p | $p>=0 and $p<=32 and $p==($p|floor) else true end);"rule.destination") else . end) |
   need((.port|type)=="string";"rule.port") |
   need((.port=="" or (.protocol!="any" and (.port|split("-")|length<=2 and all(.[];digits and length<=5))));"rule.port") |
   (if .port!="" then (.port|split("-")|map(tonumber)) as $p | need($p[0]>=1 and $p[-1]<=65535 and $p[0]<=$p[-1];"rule.port") else . end) |
   {name,enabled,network,mac:(.mac|ascii_downcase),destination,protocol,port}],
 services:((.services // []) | need(type=="array" and length<=64;"services") | map(
   need((.name|textok) and (.enabled|type)=="boolean";"service.name") |
   need(.protocol=="tcp" or .protocol=="udp" or .protocol=="both";"service.protocol") |
   need((.port|type)=="string" and (.port|split("-")|length<=2 and all(.[];digits and length<=5));"service.port") |
   (.port|split("-")|map(tonumber)) as $p |
   need($p[0]>=1 and $p[-1]<=65535 and $p[0]<=$p[-1];"service.port") |
   need((.source|type)=="string";"service.source") |
   (if .source!="" then (.source|split("/")) as $a | ($a[0]|ipnum) as $ip |
     need(($a|length)<=2 and (if ($a|length)==2 then ($a[1]|tonumber) as $n | $n>=0 and $n<=32 and $n==($n|floor) else true end);"service.source") else . end) |
   {name,enabled,protocol,port,source}))};
try {ok:true,config:(if $kind=="lan" then lan else firewall end)} catch {ok:false,error:("invalid_"+$kind),field:.}
