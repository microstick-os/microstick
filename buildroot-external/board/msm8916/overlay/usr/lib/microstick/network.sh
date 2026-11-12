# Shared network operations. Tests may override paths and commands before sourcing.
: "${MICROSTICK_RUN:=/run/microstick}"
: "${MICROSTICK_ETC:=/etc/microstick}"
: "${MICROSTICK_SYS:=/sys}"
: "${MICROSTICK_IP:=ip}"
: "${MICROSTICK_MMCLI:=mmcli}"

microstick_error() {
    printf '%s\n' "$*" > "$MICROSTICK_RUN/error"
    logger -t microstick "$*"
}

lte_error() {
    printf '%s\n' "$1" > "$MICROSTICK_RUN/lte-error"
    microstick_error "$1"
}

valid_iface() {
    case "$1" in wwan[0-9]*) case "${1#wwan}" in *[!0-9]*) return 1;; esac;; *) return 1;; esac
    [ -d "$MICROSTICK_SYS/class/net/$1" ]
}

# The LAN configuration is independent of WAN policy and shared by all consumers.
load_lan() {
    local cfg
    cfg="$MICROSTICK_ETC/lan.json"
    if [ -f "$MICROSTICK_RUN/lan-pending.json" ]; then
        USB_ADDRESS=$(jq -er '.config.usb.address' "$MICROSTICK_RUN/lan-pending.json")
        AP_ADDRESS=$(jq -er '.config.ap.address' "$MICROSTICK_RUN/lan-pending.json")
    elif [ -f "$cfg" ]; then
        USB_ADDRESS=$(jq -er '.usb.address' "$cfg")
        AP_ADDRESS=$(jq -er '.ap.address' "$cfg")
    fi
}

# Return failure before changing addresses/routes if the upstream overlaps a LAN.
check_overlap() {
    local upstream prefix
    load_lan
    upstream=$1 prefix=$2
    case "$prefix" in ''|*[!0-9]*) return 1;; esac
    [ "$prefix" -ge 1 ] && [ "$prefix" -le 32 ] || return 1
    jq -en --arg upstream "$upstream" --argjson prefix "$prefix" \
      --arg usb "$USB_ADDRESS" --arg ap "$AP_ADDRESS" '
      def ipnum: split(".") | if length != 4 then error("IPv4") else . end |
        map(tonumber | if . < 0 or . > 255 or floor != . then error("IPv4") else . end) |
        reduce .[] as $n (0; . * 256 + $n);
      ($upstream | ipnum) as $u |
      all([$usb, $ap][]; split("/") as $lan |
        ([$prefix, ($lan[1]|tonumber)] | min) as $bits |
        pow(2; 32-$bits) as $block |
        (($u/$block)|floor) != ((($lan[0]|ipnum)/$block)|floor))' >/dev/null
}

clear_lte() {
    local iface
    if [ -r "$MICROSTICK_RUN/lte-iface" ]; then
        iface=$(cat "$MICROSTICK_RUN/lte-iface")
        if valid_iface "$iface"; then
            "$MICROSTICK_IP" -4 route flush dev "$iface" proto 186 || :
            "$MICROSTICK_IP" -4 addr flush dev "$iface" || :
            [ ! -r "$MICROSTICK_RUN/dhcp-$iface.pid" ] || kill "$(cat "$MICROSTICK_RUN/dhcp-$iface.pid")" 2>/dev/null || :
        fi
    fi
    rm -f "$MICROSTICK_RUN/lte-iface" "$MICROSTICK_RUN/lte-dns" "$MICROSTICK_RUN/lte-applied"
}

apply_bearer() {
    local bearer_json iface method addr prefix mtu gateway
    bearer_json=$1
    iface=$(jq -er '.bearer.status.interface' "$bearer_json") || return 1
    valid_iface "$iface" || { microstick_error 'Invalid LTE data interface'; return 1; }
    [ "$(jq -r '.bearer.status.connected' "$bearer_json")" = yes ] || return 1
    method=$(jq -r '.bearer["ipv4-config"].method' "$bearer_json")
    case "$method" in
      static)
        addr=$(jq -er '.bearer["ipv4-config"].address' "$bearer_json") || return 1
        prefix=$(jq -er '.bearer["ipv4-config"].prefix | tonumber' "$bearer_json") || return 1
        check_overlap "$addr" "$prefix" || { microstick_error 'LTE address invalid or overlaps LAN'; return 1; }
        ;;
      dhcp) ;;
      *) microstick_error "Unsupported bearer IPv4 method: $method"; return 1 ;;
    esac
    clear_lte
    printf '%s\n' "$iface" > "$MICROSTICK_RUN/lte-iface"
    "$MICROSTICK_IP" link set dev "$iface" up || return 1
    mtu=$(jq -r '.bearer["ipv4-config"].mtu // "0" | tonumber' "$bearer_json") || return 1
    if [ "$mtu" -gt 0 ]; then
        [ "$mtu" -ge 576 ] && [ "$mtu" -le 9000 ] || return 1
        "$MICROSTICK_IP" link set dev "$iface" mtu "$mtu" || return 1
    fi
    if [ "$method" = dhcp ]; then
        udhcpc -b -i "$iface" -p "$MICROSTICK_RUN/dhcp-$iface.pid" -s /usr/libexec/microstick-dhcp 9>&-
    else
        "$MICROSTICK_IP" -4 addr replace "$addr/$prefix" dev "$iface" || return 1
        gateway=$(jq -r '.bearer["ipv4-config"].gateway // empty | select(. != "--")' "$bearer_json")
        if [ -n "$gateway" ]; then
            "$MICROSTICK_IP" -4 route replace default via "$gateway" dev "$iface" onlink metric "$LTE_METRIC" proto 186 || return 1
        else
            "$MICROSTICK_IP" -4 route replace default dev "$iface" metric "$LTE_METRIC" proto 186 || return 1
        fi
        jq -r '.bearer["ipv4-config"].dns // [] | .[] | "nameserver " + .' "$bearer_json" > "$MICROSTICK_RUN/lte-dns"
    fi
    jq -S '{status: .bearer.status, ipv4: .bearer["ipv4-config"]}' "$bearer_json" > "$MICROSTICK_RUN/lte-applied"
    rm -f "$MICROSTICK_RUN/error"
}

poll_lte() {
    local modem state unlock bearer properties profile request resolved reason rc
    if [ "$LTE_ENABLED" != yes ]; then
        clear_lte
        if [ -r "$MICROSTICK_RUN/lte-error" ] && cmp -s "$MICROSTICK_RUN/lte-error" "$MICROSTICK_RUN/error"; then rm -f "$MICROSTICK_RUN/error"; fi
        rm -f "$MICROSTICK_RUN/lte-error" "$MICROSTICK_RUN/lte-connecting"
        return 0
    fi
    if [ "${MICROSTICK_CACHED_MODEM:-no}" = yes ]; then
        cp "$MICROSTICK_RUN/modem-control.json" "$MICROSTICK_RUN/modem.json"
        modem=$(jq -er '.modem["dbus-path"]' "$MICROSTICK_RUN/modem.json") || return 1
    else
    modem=$("$MICROSTICK_MMCLI" -L -J 2>/dev/null | jq -er '.["modem-list"][0]') || {
        clear_lte; lte_error modem_unavailable; return 1;
    }
    "$MICROSTICK_MMCLI" -m "$modem" -J > "$MICROSTICK_RUN/modem.json" 2>/dev/null || { clear_lte; lte_error modem_unavailable; return 1; }
    fi
    state=$(jq -r '.modem.generic.state' "$MICROSTICK_RUN/modem.json")
    unlock=$(jq -r '.modem.generic["unlock-required"] // "none"' "$MICROSTICK_RUN/modem.json")
    reason=$(jq -r '.modem.generic["state-failed-reason"] // ""' "$MICROSTICK_RUN/modem.json")
    if [ "$reason" = sim-missing ]; then clear_lte; lte_error sim_missing; return 1; fi
    # Match ModemManager's simple-connect semantics: PIN2/PUK2 protect
    # supplementary SIM features and do not block a mobile data connection.
    case "$unlock" in
      none|--|sim-pin2|sim-puk2) ;;
      sim-pin) clear_lte; lte_error sim_pin_required; return 1 ;;
      sim-puk) clear_lte; lte_error sim_puk_required; return 1 ;;
      *) clear_lte; lte_error sim_locked; return 1 ;;
    esac
    if [ "$state" = failed ] || [ "$state" = locked ]; then
        reason=$(jq -r '.modem.generic["state-failed-reason"]' "$MICROSTICK_RUN/modem.json")
        clear_lte
        case "$reason" in sim-missing) lte_error sim_missing;; *) lte_error modem_failed;; esac
        return 1
    fi
    bearer=$(jq -r '.modem.generic.bearers[0] // empty' "$MICROSTICK_RUN/modem.json")
    if [ -f "$MICROSTICK_RUN/lte-profile-changed" ]; then
        "$MICROSTICK_MMCLI" -m "$modem" --simple-disconnect >/dev/null 2>&1 || { lte_error lte_connect_failed; return 1; }
        rm -f "$MICROSTICK_RUN/lte-profile-changed"
        bearer=''
    fi
    if [ -n "$bearer" ]; then
        if [ "${MICROSTICK_CACHED_MODEM:-no}" = yes ]; then
            cp "$MICROSTICK_RUN/bearer-control.json" "$MICROSTICK_RUN/bearer.json"
        else
        "$MICROSTICK_MMCLI" -b "$bearer" -J > "$MICROSTICK_RUN/bearer.json" 2>/dev/null || { clear_lte; lte_error modem_unavailable; return 1; }
        fi
        if [ "$(jq -r '.bearer.status.connected' "$MICROSTICK_RUN/bearer.json")" = yes ]; then
            jq -S '{status: .bearer.status, ipv4: .bearer["ipv4-config"]}' "$MICROSTICK_RUN/bearer.json" > "$MICROSTICK_RUN/bearer-settings.json"
            if [ ! -r "$MICROSTICK_RUN/lte-applied" ] || ! cmp -s "$MICROSTICK_RUN/bearer-settings.json" "$MICROSTICK_RUN/lte-applied"; then
                apply_bearer "$MICROSTICK_RUN/bearer.json" || { clear_lte; lte_error lte_ip_failed; return 1; }
            fi
            rm -f "$MICROSTICK_RUN/lte-error" "$MICROSTICK_RUN/error"
            return 0
        fi
    fi
    clear_lte
    properties=$(sed '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$MICROSTICK_ETC/lte.properties")
    profile=''
    request="$MICROSTICK_ETC/lte.json"
    if [ -s "$request" ]; then
        profile=$(jq -er '.profile // (if (.apn // "") != "" then "manual" else "auto" end)' "$request") || { lte_error invalid_lte_parameters; return 1; }
    elif [ -z "$properties" ]; then
        profile=auto
        request="$MICROSTICK_RUN/lte-profile-request.json"
        printf '{"profile":"auto"}\n' > "$request"
    fi
    resolved="$MICROSTICK_RUN/lte-resolved-private.json"
    if [ -n "$profile" ] && [ "$profile" != manual ]; then
        rc=0
        timeout 20 "${MICROSTICK_MODEM:-/usr/libexec/microstick-modem}" profile < "$request" > "$resolved" || rc=$?
        if jq -e 'type=="object" and (.state|type)=="string"' "$resolved" >/dev/null 2>&1; then
            jq '{profile,profile_id,provider,effective_apn:.apn,state,error}' "$resolved" > "$MICROSTICK_RUN/lte-profile.json.new"
            mv "$MICROSTICK_RUN/lte-profile.json.new" "$MICROSTICK_RUN/lte-profile.json"
        fi
        if [ "$rc" != 0 ] || ! jq -e '.ok == true and (.apn|type)=="string" and (.apn|length)>0' "$resolved" >/dev/null 2>&1; then
            reason=$(jq -er '.error | select(type=="string" and length>0)' "$resolved" 2>/dev/null) || reason=apn_required
            rm -f "$resolved"
            lte_error "$reason"; return 1
        fi
        request="$resolved"
    fi
    if [ -n "$profile" ]; then
        touch "$MICROSTICK_RUN/lte-connecting"
        rc=0
        timeout 120 "${MICROSTICK_MODEM:-/usr/libexec/microstick-modem}" connect < "$request" > "$MICROSTICK_RUN/lte-result.json" || rc=$?
        rm -f "$resolved"
        rm -f "$MICROSTICK_RUN/lte-connecting"
        if [ "$rc" != 0 ]; then
            reason=$(jq -er '.error | select(type=="string" and length>0)' "$MICROSTICK_RUN/lte-result.json" 2>/dev/null) || reason=lte_connect_failed
            [ "$rc" != 124 ] || reason=lte_timeout
            lte_error "$reason"; return 1
        fi
        rm -f "$MICROSTICK_RUN/lte-error" "$MICROSTICK_RUN/error"
        return 0
    fi
    if [ -z "$properties" ]; then lte_error apn_required; return 1; fi
    case "$properties" in *pin=*|*puk=*) lte_error sim_code_in_config; return 1;; esac
    case ",$properties," in
      *,ip-type=ipv4,*) ;;
      *,ip-type=*) lte_error lte_ip_failed; return 1 ;;
      *) properties="$properties,ip-type=ipv4" ;;
    esac
    touch "$MICROSTICK_RUN/lte-connecting"
    rc=0
    timeout 120 "$MICROSTICK_MMCLI" -m "$modem" --simple-connect="$properties" >/dev/null 2>"$MICROSTICK_RUN/lte-mmcli-error" || rc=$?
    rm -f "$MICROSTICK_RUN/lte-connecting"
    reason=lte_connect_failed
    # Inspect only this fixed protocol identifier; never expose remote text.
    if grep -q 'org.freedesktop.libqmi.Error.Protocol.DeviceNotReady' "$MICROSTICK_RUN/lte-mmcli-error"; then reason=modem_not_ready; fi
    rm -f "$MICROSTICK_RUN/lte-mmcli-error"
    [ "$rc" != 124 ] || reason=lte_timeout
    if [ "$rc" != 0 ]; then lte_error "$reason"; return 1; fi
    rm -f "$MICROSTICK_RUN/lte-error" "$MICROSTICK_RUN/error"
}

: "${WIFI_INTERFACE:=$(cat "$MICROSTICK_RUN/wifi-interface" 2>/dev/null || :)}"
[ "$WIFI_INTERFACE" != unavailable ] || WIFI_INTERFACE=''
if [ -z "$WIFI_INTERFACE" ]; then
 for n in "$MICROSTICK_SYS"/class/net/*; do
  [ -d "$n/phy80211" ] || continue
  WIFI_INTERFACE=${n##*/}; break
 done
fi
: "${WIFI_INTERFACE:=unavailable}"
: "${USB_INTERFACE:=$(cat "$MICROSTICK_RUN/usb-iface" 2>/dev/null || :)}"
: "${USB_INTERFACE:=unavailable}"
