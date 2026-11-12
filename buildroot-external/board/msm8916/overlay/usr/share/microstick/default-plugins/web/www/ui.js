'use strict';
const pageSections={overview:[],lte:['connection','identity'],wifi:['hotspot','uplink'],lan:['devices','dhcp','reservations','ports','firewall'],statistics:['lte','wifi'],settings:['device','access']};
let currentPage='overview',currentSub='',menuTrigger=null;
const rememberedSections={};
function signalView(id,value,recent){
 const box=document.getElementById(id);if(!box)return;
 const n=value!==undefined&&value!==null&&value!==''&&Number.isFinite(Number(value))?Math.max(0,Math.min(100,Number(value))):null;
 box.replaceChildren();box.classList.toggle('stale',recent==='no');
 const bars=n===null?0:n===0?0:Math.min(4,Math.ceil(n/25));
 for(let i=0;i<4;i++){const b=document.createElement('i');if(i<bars)b.className='on';box.append(b);}
 const label=n===null?'—':Math.round(n)+'%';const span=document.createElement('span');span.textContent=label;box.append(span);
 box.setAttribute('aria-label',t('signal_quality')+': '+label+(recent==='no'?' · '+t('stale_signal'):''));box.title=box.getAttribute('aria-label');
}
function carrierView(element,name,code){
 const raw=(name&&name!=='--'?name:'').trim();let brand='';
 const known=[['yota',/yota|йота/i],['megafon',/mega.?fon|мегафон/i],['mts',/^mts$|мтс/i],['beeline',/beeline|билайн/i],['t2',/^t2$|tele2|теле2/i]];
 for(const [key,pattern] of known)if(pattern.test(raw)){brand=key;break;}
 if(!raw)brand=({'25001':'mts','25002':'megafon','25020':'t2','25099':'beeline'})[code]||'';
 const fallback={yota:'Yota',megafon:'МегаФон',mts:'МТС',beeline:'билайн',t2:'t2'};
 element.textContent=raw||fallback[brand]||t('mobile_network');element.className='carrier'+(brand?' '+brand:'');
}
function radioName(technologies){
 const map={'gsm':'2G','gsm-compact':'2G','gprs':'G · 2G','edge':'E · 2G','umts':'3G','hsdpa':'H · 3G','hsupa':'H · 3G','hspa':'H · 3G','hspa-plus':'H+ · 3G','hspa+':'H+ · 3G','lte':'4G / LTE','5gnr':'5G'};
 const order=['5gnr','lte','hspa+','hspa-plus','hspa','hsupa','hsdpa','umts','edge','gprs','gsm','gsm-compact'];
 const values=Array.isArray(technologies)?technologies:[];return map[order.find(k=>values.includes(k))]||t('radio_unknown');
}
function renderConnection(s){
 const g=s.modem?.modem?.generic||{},gsm=s.modem?.modem?.['3gpp']||{},v=lteView(s),isLte=s.uplink?.startsWith('wwan'),active=!!s.uplink;
 carrierView($('operator'),gsm['operator-name'],gsm['operator-code']);
 $('radio').textContent=radioName(g['access-technologies']);
 signalView('mobilesignal',g['signal-quality']?.value,g['signal-quality']?.recent);
 $('simstate').textContent=t(v.code);$('ltedetail').hidden=['connected','registered','searching','modem_starting'].includes(v.code)&&!v.problem&&v.hint!=='mobile_standby_hint';
 $('ltedetail').textContent=t(v.hint);
 $('pinform').hidden=v.code!=='sim_pin_required';$('pinretries').textContent=v.remaining!=null?t('retries')+v.remaining:t('retries_unknown');$('pinsubmit').disabled=v.remaining===0;
 $('lteretry').hidden=!s.lte_enabled||!v.problem||['sim_pin_required','sim_puk_required','sim_locked','sim_missing'].includes(v.code);
 $('lteattention').hidden=!s.lte_enabled||!v.problem||currentPage==='lte';$('ltetitle').textContent=t(v.code);$('ltehint').textContent=t(v.hint);$('mobileaction').textContent=t(v.code==='sim_pin_required'?'enterpin':'mobileaction');
 $('uplink').textContent=active?(isLte?radioName(g['access-technologies']):'Wi-Fi'):t('offline');
 if(isLte){carrierView($('overviewoperator'),gsm['operator-name'],gsm['operator-code']);signalView('overviewsignal',g['signal-quality']?.value,g['signal-quality']?.recent);}
 else{$('overviewoperator').className='carrier';$('overviewoperator').textContent=active?s.ssid||'Wi-Fi':t('offline');$('overviewsignal').replaceChildren();}
 $('linkdetail').textContent=active?(!isLte&&s.wifi_connection?.pending?t('wifi_verifying'):t('connected')):s.lte_enabled?t(v.code):t('waiting');
 $('overviewwifi').textContent=s.wifi==='ap'?(t('hotspot')+(window.panelSSID?' · '+window.panelSSID:'')):s.wifi==='sta'?(t('wifi_client')+(s.ssid?' · '+s.ssid:'')):t('off');
 const d=s.device||{},fqdn=d.mdns_name||'microstick.local';
 if(d.mdns_ready&&location.hostname.endsWith('.local')&&location.hostname!==fqdn)location.replace('http://'+fqdn+'/');
 $('mdnsstatus').textContent=fqdn+(d.mdns_ready?'':' · '+t('mdns_waiting'));
 const guard=s.wifi_connection||{};$('wifiguard').hidden=!guard.pending&&!['captive_portal','no_internet','wifi_access_unconfirmed','wifi_save_failed'].includes(guard.connectivity);
 $('wifiguard').textContent=guard.pending?t('wifi_pending')+' '+Math.ceil(guard.remaining||0)+' '+t('seconds')+' · http://'+fqdn+'/':t(guard.connectivity||'');
 $('addresses').replaceChildren();const localLink=document.createElement('a');localLink.href='http://'+fqdn+'/';localLink.textContent=fqdn;$('addresses').append(localLink);
 for(const a of s.addresses||[]){if(a.ifname==='lo'||/^wwan/.test(a.ifname))continue;
  const role=/^usb/.test(a.ifname)?'USB':/^wlan|^wlp/.test(a.ifname)?(s.wifi==='ap'?'Wi-Fi AP':s.wifi==='sta'?'Wi-Fi':''):'';if(!role)continue;
  for(const addr of a.addr_info||[]){if(addr.family!=='inet')continue;const link=document.createElement('a');link.href='http://'+addr.local+'/';link.textContent=addr.local;const small=document.createElement('small');small.textContent=role;link.append(small);$('addresses').append(link);}
 }
}
function closeMenu(){const wasOpen=document.body.classList.contains('menu-open');document.body.classList.remove('menu-open');$('scrim').hidden=true;$('menutoggle').setAttribute('aria-expanded','false');if(innerWidth<=860)$('sidebar').inert=true;if(wasOpen)menuTrigger?.focus();}
function navigatePanel(page,sub,replace=false){
 if(!Object.hasOwn(pageSections,page))page='overview';
 const sections=pageSections[page];sub=sections.includes(sub)?sub:rememberedSections[page]||sections[0]||'';
 currentPage=page;currentSub=sub;rememberedSections[page]=sub;
 document.querySelectorAll('[data-tab]').forEach(b=>{const selected=b.dataset.tab===page;b.classList.toggle('selected',selected);if(selected)b.setAttribute('aria-current','page');else b.removeAttribute('aria-current');});
 document.querySelectorAll('.page').forEach(p=>p.hidden=p.id!==page);
 document.querySelectorAll('[data-panel]').forEach(p=>{if(p.dataset.panel.startsWith(page+'/'))p.hidden=p.dataset.panel!==page+'/'+sub;});
 document.querySelectorAll('[data-sub]').forEach(b=>{if(b.dataset.page!==page)return;const selected=b.dataset.sub===sub;b.classList.toggle('selected',selected);b.setAttribute('aria-selected',String(selected));});
 $('headline').textContent=t(page);document.title=t(page)+' · microstick';
 const hash='#/'+page+(sub?'/'+sub:'');if(location.hash!==hash){if(replace)history.replaceState(null,'',hash);else history.pushState(null,'',hash);}
 if(latest.wifi)renderConnection(latest);closeMenu();if(typeof refreshStatistics==='function')refreshStatistics(true);
}
function selectSubTab(page,sub){navigatePanel(page,sub);if(page==='lan'&&typeof refreshLan==='function')refreshLan();}
document.addEventListener('DOMContentLoaded',()=>{
 Object.assign(ru,{hotspot:'Точка доступа',signin:'Вход в microstick',loginhint:'Изначально пароль панели совпадает с паролем точки доступа.',settings:'Система',internet:'Интернет',statistics:'Статистика',open_section:'Открыть',wifimode:'Режим Wi-Fi',known_devices:'Известные устройства',wifi_client:'Клиент',current_speed:'Текущая скорость',download:'Приём',upload:'Отдача',mobile_traffic:'Мобильный трафик',since_reset:'С последнего сброса',uptime:'Время работы',connection_settings:'Подключение',ttl_imei:'TTL и IMEI',wifi_uplink:'Wi-Fi как интернет',dhcp_addresses:'DHCP и адреса',reservations_tab:'Закрепления',current_period:'Текущий период мобильной сети',reset_statistics:'Сбросить период',last_15_minutes:'Последние 15 минут',lte_history:'История мобильной сети',by_day:'По дням',by_month:'По месяцам',wifi_session:'Сессия Wi-Fi',wifi_memory_hint:'Только текущее подключение. Данные очищаются при разрыве связи или перезагрузке.',device_status:'Устройство',cpu_load:'Загрузка CPU',memory:'Память',modem_temperature:'Температура модема',signal_quality:'Качество сигнала',stale_signal:'Данные не обновлены',mobile_network:'Мобильная сеть',connected_count:'Подключено к AP',recent_count:'Недавняя активность',statistics_unavailable:'Плагин статистики недоступен',no_samples:'Пока недостаточно данных для графика',no_history:'История пока пуста',since:'С ',date_unknown:'Время не синхронизировано',confirm_statistics:'Обнулить текущий период мобильной сети? История по дням и месяцам сохранится.',statistics_reset:'Начат новый период мобильной сети',statistics_save_failed:'Не удалось сохранить статистику. Прежний период сохранён.',counter_unavailable:'Счётчики трафика временно недоступны',invalid_statistics_request:'Некорректный запрос статистики',history_date:'Дата',timezone:'Время устройства',statistics_loading:'Ожидание статистики',discard_edits:'Есть несохранённые изменения',access:'Адреса панели'});
 Object.assign(en,{hotspot:'Hotspot',signin:'Sign in to microstick',loginhint:'Initially, the panel password matches the hotspot password.',settings:'System',internet:'Internet',statistics:'Statistics',open_section:'Open',wifimode:'Wi-Fi mode',known_devices:'Known devices',wifi_client:'Client',current_speed:'Current speed',download:'Download',upload:'Upload',mobile_traffic:'Mobile traffic',since_reset:'Since last reset',uptime:'Uptime',connection_settings:'Connection',ttl_imei:'TTL & IMEI',wifi_uplink:'Wi-Fi uplink',dhcp_addresses:'DHCP & addresses',reservations_tab:'Reservations',current_period:'Current mobile period',reset_statistics:'Reset period',last_15_minutes:'Last 15 minutes',lte_history:'Mobile history',by_day:'By day',by_month:'By month',wifi_session:'Wi-Fi session',wifi_memory_hint:'For this connection only. Cleared when disconnected or restarted.',device_status:'Device',cpu_load:'CPU load',memory:'Memory',modem_temperature:'Modem temperature',signal_quality:'Signal quality',stale_signal:'Data is not recent',mobile_network:'Mobile network',connected_count:'Connected to AP',recent_count:'Recent activity',statistics_unavailable:'Statistics plugin is unavailable',no_samples:'Waiting for graph samples',no_history:'No history yet',since:'Since ',date_unknown:'Time is not synchronized',confirm_statistics:'Reset the current mobile period? Daily and monthly history will be kept.',statistics_reset:'New mobile period started',statistics_save_failed:'Could not save statistics. The previous period is retained.',counter_unavailable:'Traffic counters temporarily unavailable',invalid_statistics_request:'Invalid statistics request',history_date:'Date',timezone:'Device time',statistics_loading:'Waiting for statistics',discard_edits:'There are unsaved changes',access:'Panel addresses'});
 // Short, actionable error messages; raw modem diagnostics stay out of the status card.
 ru.modem_not_ready='Не удалось запустить модем';ru.modem_not_ready_hint='Перезагрузите устройство. Если ошибка повторяется, нужна диагностика модема.';
 en.modem_not_ready='Could not start the modem';en.modem_not_ready_hint='Restart the device. If this repeats, the modem needs diagnostics.';
 document.querySelectorAll('[data-sub]').forEach(b=>b.onclick=()=>selectSubTab(b.dataset.page,b.dataset.sub));
 document.querySelectorAll('[data-go]').forEach(b=>b.onclick=()=>{const [p,s]=b.dataset.go.split('/');selectSubTab(p,s);});
 $('menutoggle').onclick=()=>{menuTrigger=$('menutoggle');$('sidebar').inert=false;document.body.classList.add('menu-open');$('scrim').hidden=false;$('menutoggle').setAttribute('aria-expanded','true');$('sidebar').querySelector('.selected')?.focus();};$('scrim').onclick=closeMenu;
 document.addEventListener('keydown',e=>{if(!document.body.classList.contains('menu-open'))return;if(e.key==='Escape'){e.preventDefault();closeMenu();}if(e.key==='Tab'){const all=[...$('sidebar').querySelectorAll('a[tabindex],button')].filter(e=>!e.disabled);const first=all[0],last=all.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}});
 window.addEventListener('resize',()=>{$('sidebar').inert=innerWidth<=860&&!document.body.classList.contains('menu-open');if(innerWidth>860)closeMenu();});$('sidebar').inert=innerWidth<=860;
 function restoreRoute(){const [page,sub]=location.hash.replace(/^#\//,'').split('/');selectSubTab(Object.hasOwn(pageSections,page)?page:'overview',sub);}
 window.addEventListener('popstate',restoreRoute);restoreRoute();language();
 document.querySelectorAll('[data-t-title]').forEach(e=>e.title=t(e.dataset.tTitle));
 window.addEventListener('beforeunload',e=>{if(typeof lanDirty!=='undefined'&&(lanDirty||firewallDirty||servicesDirty)){e.preventDefault();e.returnValue='';}});
});
