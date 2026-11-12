'use strict';
Object.assign(ru,{
 lan:'Локальная сеть',lanhint:'Локальная сеть настраивается независимо от SIM и источника интернета. В режиме Wi-Fi-клиента DHCP-сервер работает только на USB.',
 devices:'Устройства',device:'Устройство',connectiontype:'Подключение',activity:'Активность',lease:'Аренда',actions:'Действия',clienthint:'Аренда DHCP не означает, что устройство сейчас в сети. Имена и активность получены от устройств и локальной сети.',
 dhcpsettings:'DHCP и локальные адреса',lanchangehint:'Смена адреса стика или отключение DHCP требует подтверждения за 2 минуты. Переподключитесь или обновите IP и откройте новый адрес панели. Без подтверждения прежние настройки вернутся автоматически.',
 reservations:'Имена и закреплённые адреса',reservationhint:'Оставьте IP пустым, чтобы сохранить только имя. Закреплённый адрес будет выдан при обновлении DHCP на клиенте.',adddevice:'Добавить устройство',discard:'Отменить правки',
 accessrules:'Правила доступа в интернет',firewallhint:'Выберите устройство, чтобы запретить ему интернет, и сохраните правило. Для отдельных сайтов или сервисов откройте расширенные настройки. Доступ к локальной панели сохранится.',addrule:'Добавить правило',
 confirm_lan_title:'Подтвердите настройки сети',keepchanges:'Оставить настройки',revert:'Вернуть прежние',remaining:'До возврата прежних настроек, секунд: ',previous:'Назад',next:'Далее',
 serverenabled:'DHCP-сервер',routeraddress:'IP стика',netmask:'Маска сети',poolstart:'Первый адрес',poolend:'Последний адрес',leaseminutes:'Аренда, минуты',dnsservers:'DNS: до двух IPv4 через запятую',dnsauto:'DNS провайдера автоматически',name:'Имя',reservedip:'Закреплённый IP (необязательно)',network:'Сеть',remove:'Удалить',edit:'Имя / IP',block:'Запретить интернет',unblock:'Разрешить интернет',blocked:'Интернет запрещён',
 connected_client:'Подключено к AP',recent:'Недавно',lease_only:'Неизвестно',no_devices:'Устройства пока не обнаружены',any:'Любой',allnetworks:'USB и Wi-Fi',ruleenabled:'Правило включено',source_mac:'MAC устройства',destination:'IP сервера или подсеть',protocol:'Протокол',destinationport:'Порт сервиса',
 invalid_lan:'Проверьте настройки локальной сети',invalid_firewall:'Проверьте правило доступа',router_apply_failed:'Не удалось применить сеть. Прежние настройки восстановлены.',firewall_apply_failed:'Не удалось применить правила. Сохранены прежние.',lan_saved:'Настройки локальной сети сохранены',firewall_saved:'Правила доступа сохранены',lan_reverted:'Возвращены прежние настройки сети',lan_pending:'Откройте панель по новому адресу и подтвердите настройки за 2 минуты',no_pending_lan:'Временные настройки уже отменены или подтверждены',invalid_token:'Настройки изменились. Обновите страницу',occupied_ip:'Адрес уже принадлежит другому устройству',overlap_wan:'Локальная сеть пересекается с сетью источника интернета',overlap:'Сети USB и Wi-Fi пересекаются',duplicate_mac:'MAC повторяется в одной сети',duplicate_ip:'Закреплённый IP повторяется',unsaved_rules:'Сначала сохраните или отмените правки правил',limit_entries:'Допускается до 64 записей',no_rules:'Правила не заданы — клиентам разрешён интернет',no_reservations:'Закреплённых адресов пока нет',confirm_lan:'Применить локальные настройки? При смене IP или отключении DHCP переподключитесь и подтвердите новый адрес за 2 минуты.',wifi_inactive:'Сеть Wi-Fi AP сейчас неактивна; настройки сохраняются для следующего включения.',
});
Object.assign(en,{
 lan:'Local network',remaining:'Seconds until automatic revert: ',serverenabled:'DHCP server',routeraddress:'Stick IP',netmask:'Subnet mask',poolstart:'First address',poolend:'Last address',leaseminutes:'Lease, minutes',dnsservers:'DNS: up to two IPv4 addresses, comma separated',dnsauto:'Use provider DNS automatically',name:'Name',reservedip:'Reserved IP (optional)',network:'Network',remove:'Remove',edit:'Name / IP',block:'Block internet',unblock:'Allow internet',blocked:'Internet blocked',connected_client:'Connected to AP',recent:'Recent',lease_only:'Unknown',no_devices:'No devices detected yet',any:'Any',allnetworks:'USB and Wi-Fi',ruleenabled:'Rule enabled',source_mac:'Device MAC',destination:'Server IP or subnet',protocol:'Protocol',destinationport:'Service port',invalid_lan:'Check local network settings',invalid_firewall:'Check the access rule',router_apply_failed:'Network apply failed. Previous settings restored.',firewall_apply_failed:'Rule apply failed. Previous rules kept.',lan_saved:'Local network settings saved',firewall_saved:'Access rules saved',lan_reverted:'Previous network settings restored',lan_pending:'Open the panel at its new address and confirm within 2 minutes',no_pending_lan:'Temporary settings have already expired or been confirmed',invalid_token:'Settings changed. Refresh this page.',occupied_ip:'The address belongs to another device',overlap_wan:'Local subnet overlaps the internet uplink',overlap:'USB and Wi-Fi subnets overlap',duplicate_mac:'Duplicate MAC in the same network',duplicate_ip:'Duplicate reserved IP',unsaved_rules:'Save or discard rule edits first',limit_entries:'Up to 64 entries are allowed',no_rules:'No rules — internet access is allowed',no_reservations:'No reserved addresses yet',confirm_lan:'Apply local settings? If changing the IP or disabling DHCP, reconnect and confirm the new address within 2 minutes.',wifi_inactive:'Wi-Fi AP is inactive; these settings are saved for its next start.'
});
Object.assign(ru,{
 rule_name:'Название (необязательно)',rule_device:'Устройство',all_devices:'Все устройства',manual_device:'Указать MAC вручную',choose_network:'Выберите сеть',rule_advanced:'Расширенные настройки',
 rule_advanced_hint:'Ограничьте блокировку адресом сервера, протоколом или портом сервиса. Если указаны и устройство, и сервер, правило действует только на связь между ними. Блокируются запросы клиента в интернет и ответный трафик.',
 rule_destination_hint:'Пустое поле означает все адреса. Например, 1.1.1.1 или 1.1.1.0/24.',rule_port_hint:'Пустое поле означает все порты. Например, 443 для HTTPS или диапазон 8000-8010.',
 rule_mac_hint:'MAC можно найти на странице «Устройства». Выберите сеть подключения этого клиента.',rule_active_filters:'заданы ограничения',
});
Object.assign(en,{
 firewallhint:'Choose a device to block its internet access, then save. For selected websites or services, use advanced settings. Local panel access remains available.',
 rule_name:'Name (optional)',rule_device:'Device',all_devices:'All devices',manual_device:'Enter MAC manually',choose_network:'Choose a network',rule_advanced:'Advanced settings',
 rule_advanced_hint:'Limit blocking to a server address, protocol or service port. When both a device and server are specified, only traffic between them is blocked. This covers client requests to the internet and the replies.',
 rule_destination_hint:'Leave empty for all addresses. For example, 1.1.1.1 or 1.1.1.0/24.',rule_port_hint:'Leave empty for all ports. For example, 443 for HTTPS or a range such as 8000-8010.',
 rule_mac_hint:'Find the MAC on the Devices page. Choose the network this client connects to.',rule_active_filters:'filters applied',
});
let networkDirty=false,reservationsDirty=false,servicesDirty=false,servicesLoaded=false,servicesDraft=[];
function markLan(part,value=true){if(part==='networks')networkDirty=value;else reservationsDirty=value;lanDirty=networkDirty||reservationsDirty;}
let lanLoaded=false,firewallLoaded=false,lanDirty=false,firewallDirty=false,lanBusy=false;
let lastClientData=null,lastClientPolicy=null;
let lanState=null,lanDraft=null,firewallDraft={rules:[]},clientsPage=0,clientsTotal=0,lanPoll=false;
const newRule=()=>({name:'',enabled:true,network:'all',mac:'',destination:'',protocol:'any',port:''});
function node(tag,text,cls){const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;}
function actionButton(key,fn){const b=node('button',t(key),'quiet');b.type='button';b.onclick=async()=>{b.disabled=true;try{await fn();}catch(e){feedback(t(e.message),true);}finally{b.disabled=false;}};return b;}
function inputField(container,key,value,options={}){
 const label=node('label',t(options.label||key));let input;
 if(options.options){input=node('select');for(const [v,title] of options.options){const o=node('option',title);o.value=v;input.append(o);}}
 else{input=node('input');input.type=options.type||'text';if(options.placeholder)input.placeholder=options.placeholder;if(options.maxLength)input.maxLength=options.maxLength;}
 input.dataset.field=key;input.value=String(value??'');if(options.id)input.id=options.id;
 if(options.min!==undefined)input.min=options.min;if(options.max!==undefined)input.max=options.max;
 input.autocomplete='off';label.append(input);container.append(label);return input;
}
const toggleOptions=()=>[['true',t('on')],['false',t('off')]];
const networkOptions=(all=false)=>[...(all?[['all',t('allnetworks')]]:[]),['usb','USB'],['ap','Wi-Fi AP']];
function maskLabel(p){return Array.from({length:4},(_,i)=>Math.max(0,Math.min(255,256-Math.pow(2,Math.max(0,8-Math.max(0,p-i*8)))))).join('.')+' /'+p;}
function renderLanForm(){
 if(!lanDraft)return;$('lanfields').replaceChildren();
 for(const key of ['usb','ap']){
  const n=lanDraft[key],box=node('div',undefined,'networkeditor');box.dataset.network=key;box.append(node('h3',key==='usb'?'USB':'Wi-Fi AP'));
  if(key==='ap'&&lanState?.wifi_mode!=='ap')box.append(node('p',t('wifi_inactive'),'muted small'));
  const [ip,prefix]=n.address.split('/');
  inputField(box,'enabled',n.enabled,{label:'serverenabled',options:toggleOptions()});
  inputField(box,'address',ip,{label:'routeraddress',id:'lan-'+key+'-address'});
  inputField(box,'prefix',prefix,{label:'netmask',options:Array.from({length:23},(_,i)=>{const p=i+8;return [String(p),maskLabel(p)];})});
  inputField(box,'start',n.start,{label:'poolstart',id:'lan-'+key+'-start'});inputField(box,'end',n.end,{label:'poolend',id:'lan-'+key+'-end'});
  inputField(box,'lease',n.lease/60,{label:'leaseminutes',type:'number',min:2,max:10080,id:'lan-'+key+'-lease'});
  inputField(box,'dns',n.dns.join(', '),{label:'dnsservers',placeholder:t('dnsauto'),id:'lan-'+key+'-dns'});
  $('lanfields').append(box);
 }
 renderReservations();
}
function readLanForm(){
 if(!lanDraft)return;
 for(const box of $('lanfields').children){const get=k=>box.querySelector('[data-field="'+k+'"]').value;lanDraft[box.dataset.network]={address:get('address').trim()+'/'+get('prefix'),enabled:get('enabled')==='true',start:get('start').trim(),end:get('end').trim(),lease:Number(get('lease'))*60,dns:get('dns').split(',').map(x=>x.trim()).filter(Boolean)};}
 lanDraft.reservations=[...$('reservationrows').querySelectorAll('.editorrow')].map(row=>Object.fromEntries(['network','mac','ip','name'].map(k=>[k,row.querySelector('[data-field="'+k+'"]').value.trim()])));
}
function renderReservations(){
 $('reservationrows').replaceChildren();
 if(!lanDraft.reservations.length)$('reservationrows').append(node('p',t('no_reservations'),'muted small'));
 lanDraft.reservations.forEach((r,i)=>{const row=node('div',undefined,'editorrow'),grid=node('div',undefined,'editorgrid');
  inputField(grid,'network',r.network,{options:networkOptions()});inputField(grid,'mac',r.mac,{label:'MAC',maxLength:17});inputField(grid,'name',r.name,{maxLength:64});inputField(grid,'ip',r.ip,{label:'reservedip'});
  row.append(grid,actionButton('remove',()=>{readLanForm();lanDraft.reservations.splice(i,1);markLan('reservations');renderReservations();}));$('reservationrows').append(row);
 });
}
function readFirewallForm(){firewallDraft.rules=[...$('firewallrows').querySelectorAll('.editorrow')].map(row=>{const r={};for(const k of ['name','enabled','network','mac','destination','protocol','port'])r[k]=row.querySelector('[data-field="'+k+'"]').value.trim();r.enabled=r.enabled==='true';return r;});}
function renderFirewallForm(){
 $('firewallrows').replaceChildren();if(!firewallDraft.rules.length)$('firewallrows').append(node('p',t('no_rules'),'muted small'));
 const clients=lastClientData?.clients||[];
 firewallDraft.rules.forEach((r,i)=>{
  const row=node('div',undefined,'editorrow'),grid=node('div',undefined,'editorgrid rulegrid');
  inputField(grid,'name',r.name,{label:'rule_name',maxLength:64});inputField(grid,'enabled',r.enabled,{label:'ruleenabled',options:toggleOptions()});
  const key=c=>c.network+'/'+c.mac.toLowerCase(),known=clients.find(c=>c.network===r.network&&c.mac.toLowerCase()===r.mac.toLowerCase());
  const choices=[['',t('all_devices')],...clients.map(c=>[key(c),(c.name||c.hostname||c.mac)+' · '+(c.network==='usb'?'USB':'Wi-Fi AP')]),['manual',t('manual_device')]];
  const device=inputField(grid,'device',r.mac?(known?key(known):'manual'):'',{label:'rule_device',options:choices});
  const network=inputField(grid,'network',r.network,{options:networkOptions(!r.mac)});
  const manual=node('div',undefined,'rulemanual');const mac=inputField(manual,'mac',r.mac,{label:'source_mac',maxLength:17,placeholder:'02:12:34:56:78:90'});mac.parentElement.append(node('small',t('rule_mac_hint'),'fieldhint'));manual.hidden=device.value!=='manual';
  function networks(){const value=network.value;network.replaceChildren();for(const [v,title] of [...(device.value==='manual'?[['',t('choose_network')]]:[]),...networkOptions(device.value==='')]){const option=node('option',title);option.value=v;network.append(option);}network.value=[...network.options].some(o=>o.value===value)?value:'';network.required=device.value==='manual';mac.required=device.value==='manual';}
  networks();device.onchange=()=>{const c=clients.find(c=>key(c)===device.value);if(c){mac.value=c.mac;network.value=c.network;}else if(device.value==='')mac.value='';manual.hidden=device.value!=='manual';networks();if(!manual.hidden)mac.focus();};
  const advanced=node('details',undefined,'ruleadvanced'),hasFilters=!!(r.destination||r.port||r.protocol!=='any');advanced.open=hasFilters;
  const summary=node('summary',t('rule_advanced')+(hasFilters?' · '+t('rule_active_filters'):''));advanced.append(summary,node('p',t('rule_advanced_hint'),'muted small'));
  const filters=node('div',undefined,'editorgrid');
  const destination=inputField(filters,'destination',r.destination,{label:'destination',placeholder:'1.1.1.1'});destination.parentElement.classList.add('destinationfield');destination.parentElement.append(node('small',t('rule_destination_hint'),'fieldhint'));
  const protocol=inputField(filters,'protocol',r.protocol,{label:'protocol',options:[['any',t('any')],['tcp','TCP'],['udp','UDP'],['both','TCP + UDP']]});
  const port=inputField(filters,'port',r.port,{label:'destinationport',placeholder:'443'});port.parentElement.append(node('small',t('rule_port_hint'),'fieldhint'));
  // A port requires TCP and/or UDP. Default to both unless the user selected one.
  port.oninput=()=>{if(port.value.trim()&&protocol.value==='any')protocol.value='both';};
  protocol.onchange=()=>{if(protocol.value==='any'&&port.value.trim())protocol.value='both';};
  network.onchange=()=>{const c=clients.find(c=>key(c)===device.value);if(c&&c.network!==network.value){device.value='manual';manual.hidden=false;networks();}};
  filters.oninput=filters.onchange=()=>{summary.textContent=t('rule_advanced')+((destination.value.trim()||port.value.trim()||protocol.value!=='any')?' · '+t('rule_active_filters'):'');};
  advanced.append(filters);row.append(grid,manual,advanced,actionButton('remove',()=>{readFirewallForm();firewallDraft.rules.splice(i,1);firewallDirty=true;renderFirewallForm();}));$('firewallrows').append(row);
 });
}
function pendingView(s){
 lanState=s;const p=s.pending;$('lanpending').hidden=!p;$('lanlinks').replaceChildren();
 if(p){$('lanpendingtext').textContent=t('remaining')+p.remaining;for(const k of ['usb','ap']){const ip=s.config[k].address.split('/')[0],a=node('a',ip+' · '+(k==='usb'?'USB':'Wi-Fi AP'));a.href='http://'+ip+'/';$('lanlinks').append(a);}}
 $('lanerror').hidden=!s.error&&!s.notice;$('lanerror').textContent=t(s.error||s.notice||'');
}
async function routerAction(method,data){
 const res=await api(method,data);feedback(t('operation'),false,true);
 for(let i=0;i<120;i++){await new Promise(resolve=>setTimeout(resolve,250));const op=await api('operation');if(op.id!==res.operation)throw new Error('busy');if(!op.busy){if(op.result?.ok===false){const e=new Error(op.result.error||'router_apply_failed');e.field=op.result.field;throw e;}feedback(t(op.result?.message||'saved'));return op.result;}}
 throw new Error('wait');
}
function fieldError(formId,error){
 const formElement=$(formId),isLan=formId!=='firewallform';formElement.querySelectorAll('[aria-invalid]').forEach(e=>e.removeAttribute('aria-invalid'));
 const out=$(formId==='reservationsform'?'reservationsfieldError':isLan?'lanfieldError':'firewallfieldError');out.hidden=false;out.textContent=t(error.message)+(error.field?' · '+t(error.field):'');
 if(error.field){const parts=error.field.split('.');let targets=[];
  if(isLan&&['usb','ap'].includes(parts[0])){const fields=parts[1]==='range'?['start','end']:parts[1]==='address'?['address','prefix']:[parts[1]];targets=fields.map(k=>document.querySelector('#lanfields [data-network="'+parts[0]+'"] [data-field="'+k+'"]'));}
  else if(!isLan&&parts[0]==='rule')targets=[...$('firewallrows').querySelectorAll('[data-field="'+parts[1]+'"]')];
  targets.filter(Boolean).forEach(e=>{e.setAttribute('aria-invalid','true');const details=e.closest('details');if(details)details.open=true;});targets.find(Boolean)?.focus();
 }
}
async function refreshLan(force=false){
 if(!csrf||lanPoll)return;lanPoll=true;
 try{const [s,f,c]=await Promise.all([api('lan_config'),api('firewall_config'),api('clients?page='+clientsPage)]);
  if(s.config){pendingView(s);if(force||!lanLoaded){lanDraft=structuredClone(s.config);lanLoaded=true;networkDirty=reservationsDirty=lanDirty=false;renderLanForm();}}
  lastClientData=c;
  if(f.rules&&(force||!firewallLoaded)){firewallDraft=structuredClone(f);firewallLoaded=true;firewallDirty=false;renderFirewallForm();}
  if(force||!servicesLoaded){servicesDraft=structuredClone(f.services||[]);servicesLoaded=true;servicesDirty=false;renderServices();}
  renderClients(c,f);
 }catch(e){feedback(t(e.message),true);}finally{lanPoll=false;}
}
function fullBlock(r,c){return r.network===c.network&&r.mac.toLowerCase()===c.mac.toLowerCase()&&r.destination===''&&r.protocol==='any'&&r.port==='';}
function renderClients(data,policy){lastClientData=data;lastClientPolicy=policy;
 clientsTotal=data.total||0;$('overviewclients').textContent=String(clientsTotal);const all=data.clients||[];$('overviewactivity').textContent=clientsTotal<=32?[t('connected_count')+': '+all.filter(c=>c.activity==='connected').length,t('recent_count')+': '+all.filter(c=>c.activity==='recent').length].join(' · '):'';$('clientrows').replaceChildren();const clients=data.clients||[];
 if(!clients.length){const td=node('td',t('no_devices'));td.colSpan=6;const tr=node('tr');tr.append(td);$('clientrows').append(tr);}
 for(const c of clients){const tr=node('tr'),isBlocked=(policy.rules||[]).some(r=>r.enabled&&fullBlock(r,c));
  const title=node('td',c.name||c.hostname||c.mac);if(c.reserved)title.append(node('small',t('reservedip').replace(/\s*\(.*\)/,'')));if(isBlocked)title.append(node('small',t('blocked')));
  const addr=node('td',c.ip||'—');addr.append(node('small',c.mac));
  const exp=c.expires>0?new Date(c.expires*1000).toLocaleString(lang):'—';
  const controls=node('td'),buttons=node('div',undefined,'clientactions');
  buttons.append(actionButton('edit',()=>{readLanForm();let r=lanDraft.reservations.find(r=>r.network===c.network&&r.mac===c.mac);if(!r){if(lanDraft.reservations.length>=64)throw new Error('limit_entries');r={network:c.network,mac:c.mac,name:c.name||c.hostname||'',ip:c.ips?.[0]||''};lanDraft.reservations.push(r);}markLan('reservations');renderReservations();selectSubTab('lan','reservations');$('reservationrows').scrollIntoView({behavior:'smooth'});}),
  actionButton(isBlocked?'unblock':'block',async()=>{if(firewallDirty)throw new Error('unsaved_rules');const f=await api('firewall_config');f.rules=f.rules.filter(r=>!fullBlock(r,c));if(!isBlocked){if(f.rules.length>=64)throw new Error('limit_entries');f.rules.push({...newRule(),name:c.name||c.hostname||c.mac,network:c.network,mac:c.mac});}await routerAction('firewall',{config:f});firewallLoaded=false;await refreshLan();}));
  controls.append(buttons);tr.append(title,addr,node('td',c.network==='usb'?'USB':'Wi-Fi AP'),node('td',t(c.activity==='connected'?'connected_client':c.activity)),node('td',exp),controls);[...tr.children].forEach((cell,i)=>cell.dataset.label=t(['device','IP / MAC','connectiontype','activity','lease','actions'][i]));$('clientrows').append(tr);
 }
 $('clientpage').textContent=clientsTotal?`${clientsPage*32+1}–${Math.min((clientsPage+1)*32,clientsTotal)} / ${clientsTotal}`:'0';$('clientsprev').disabled=clientsPage===0;$('clientsnext').disabled=(clientsPage+1)*32>=clientsTotal;
}
$('lanform').addEventListener('input',()=>markLan('networks'));$('lanform').addEventListener('change',()=>markLan('networks'));
$('firewallform').addEventListener('input',()=>{firewallDirty=true;});$('firewallform').addEventListener('change',()=>{firewallDirty=true;});
async function saveLanPart(part){
 if(lanBusy)return;readLanForm();const errorId=part==='reservations'?'reservationsfieldError':'lanfieldError';$(errorId).hidden=true;
 if(part!=='reservations'&&!await askConfirmation('lan'))return;
 lanBusy=true;try{
  const current=await api('lan_config');if(current.pending)throw new Error('lan_pending');
  const candidate=structuredClone(current.config);
  if(part==='reservations')candidate.reservations=structuredClone(lanDraft.reservations);
  else{candidate.usb=structuredClone(lanDraft.usb);candidate.ap=structuredClone(lanDraft.ap);}
  await routerAction('lan',{config:candidate});const fresh=await api('lan_config');pendingView(fresh);
  // Preserve edits in the other subsection, and refresh only the saved fields.
  if(part==='reservations'){lanDraft.reservations=structuredClone(fresh.config.reservations);renderReservations();}
  else{const savedReservations=lanDraft.reservations;readLanForm();lanDraft.usb=structuredClone(fresh.config.usb);lanDraft.ap=structuredClone(fresh.config.ap);lanDraft.reservations=savedReservations;renderLanForm();}
  markLan(part,false);await refreshLan();
 }catch(e){fieldError(part==='reservations'?'reservationsform':'lanform',e);}finally{lanBusy=false;}
}
form('lanform',()=>saveLanPart('networks'));
form('reservationsform',()=>saveLanPart('reservations'));
$('reservationsform').addEventListener('input',()=>markLan('reservations'));$('reservationsform').addEventListener('change',()=>markLan('reservations'));
form('firewallform',async()=>{readFirewallForm();$('firewallfieldError').hidden=true;try{await routerAction('firewall',{config:firewallDraft});firewallLoaded=false;await refreshLan();}catch(e){fieldError('firewallform',e);}});
button('addreservation',()=>{readLanForm();if(lanDraft.reservations.length>=64)throw new Error('limit_entries');lanDraft.reservations.push({network:'ap',mac:'',name:'',ip:''});markLan('reservations');renderReservations();});
button('addrule',()=>{readFirewallForm();if(firewallDraft.rules.length>=64)throw new Error('limit_entries');firewallDraft.rules.push(newRule());firewallDirty=true;renderFirewallForm();});
button('lanreload',async()=>{readLanForm();const s=await api('lan_config');lanDraft.usb=structuredClone(s.config.usb);lanDraft.ap=structuredClone(s.config.ap);renderLanForm();markLan('networks',false);});button('reservationsreload',async()=>{readLanForm();const s=await api('lan_config');lanDraft.reservations=structuredClone(s.config.reservations);renderReservations();markLan('reservations',false);});button('firewallreload',async()=>{firewallLoaded=false;await refreshLan();});
button('clientsrefresh',()=>refreshLan());button('clientsprev',async()=>{clientsPage=Math.max(0,clientsPage-1);await refreshLan();});button('clientsnext',async()=>{clientsPage++;await refreshLan();});
button('lanconfirm',async()=>{if(!lanState?.pending)return;await routerAction('lan_confirm',{token:lanState.pending.token});await refreshLan();});
button('lancancel',async()=>{await routerAction('lan_cancel',{});lanLoaded=false;await refreshLan();});
const originalSelectTab=selectTab;selectTab=function(tab){originalSelectTab(tab);if(tab==='lan')refreshLan();};
const originalLanguage=language;language=function(){if(lanLoaded)readLanForm();if(firewallLoaded)readFirewallForm();const messageKey=Object.keys(en).find(k=>en[k]===$('feedback').textContent||ru[k]===$('feedback').textContent);originalLanguage();if(messageKey)$('feedback').textContent=t(messageKey);if(lanLoaded)renderLanForm();if(firewallLoaded)renderFirewallForm();if(lanState)pendingView(lanState);if(lastClientData)renderClients(lastClientData,lastClientPolicy);if(!$('lan').hidden)refreshLan();};
language();setInterval(()=>{if(!document.hidden&&(!$('lan').hidden||!$('overview').hidden||lanState?.pending))refreshLan();},3000);

function readServices(){servicesDraft=[...$('servicerows').querySelectorAll('.editorrow')].map(row=>{const r={};for(const k of ['name','enabled','protocol','port','source'])r[k]=row.querySelector('[data-field="'+k+'"]').value.trim();r.enabled=r.enabled==='true';return r;});}
function renderServices(){
 $('servicerows').replaceChildren();if(!servicesDraft.length)$('servicerows').append(node('p',t('no_ports'),'muted small'));
 servicesDraft.forEach((r,i)=>{const row=node('div',undefined,'editorrow'),grid=node('div',undefined,'servicegrid');
 inputField(grid,'name',r.name,{label:'rule_name',maxLength:64});inputField(grid,'enabled',r.enabled,{label:'ruleenabled',options:toggleOptions()});
 inputField(grid,'protocol',r.protocol,{label:'protocol',options:[['tcp','TCP'],['udp','UDP'],['both','TCP + UDP']]});
 inputField(grid,'port',r.port,{label:'destinationport',placeholder:'8000-8010',maxLength:11});
 const source=inputField(grid,'source',r.source,{label:'port_source',placeholder:'192.168.1.0/24',maxLength:18});source.parentElement.append(node('small',t('port_source_hint'),'fieldhint'));
 row.append(grid,actionButton('remove',()=>{readServices();servicesDraft.splice(i,1);servicesDirty=true;renderServices();}));$('servicerows').append(row);});
}
$('servicesform').addEventListener('input',()=>{servicesDirty=true;});$('servicesform').addEventListener('change',()=>{servicesDirty=true;});
button('addservice',()=>{readServices();if(servicesDraft.length>=64)throw new Error('limit_entries');servicesDraft.push({name:'',enabled:true,protocol:'both',port:'',source:''});servicesDirty=true;renderServices();});
button('servicesreload',async()=>{const f=await api('firewall_config');servicesDraft=structuredClone(f.services||[]);servicesDirty=false;renderServices();});
form('servicesform',async()=>{readServices();$('serviceserror').hidden=true;try{const f=await api('firewall_config');f.services=structuredClone(servicesDraft);await routerAction('firewall',{config:f});firewallDraft.services=structuredClone(servicesDraft);servicesDirty=false;await refreshLan();}catch(e){$('serviceserror').textContent=t(e.message);$('serviceserror').hidden=false;}});
const servicesLanguage=language;language=function(){if(servicesLoaded)readServices();servicesLanguage();if(servicesLoaded)renderServices();};
