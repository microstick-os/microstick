'use strict';
const $=id=>document.getElementById(id);
const ru={disabled:'Мобильная сеть выключена',enabled:'Готов к регистрации',enabling:'Запуск модема',disabling:'Остановка модема',disconnecting:'Отключение',login:'Войдите повторно',signin:'Добро пожаловать в microstick',loginhint:'Введите пароль панели. Изначально он совпадает с паролем точки доступа.',panelpassword:'Пароль панели',signinbutton:'Войти',logout:'Выйти',control:'УПРАВЛЕНИЕ ПОДКЛЮЧЕНИЕМ',overview:'Обзор',wifi:'Wi-Fi',lte:'Мобильная сеть',settings:'Настройки',source:'ИСТОЧНИК ИНТЕРНЕТА',preference:'Режим подключения',auto:'Автоматически',apply:'Применить',access:'АДРЕСА ПАНЕЛИ',addresshint:'В режиме клиента новый IP можно найти в списке устройств роутера. Доступ по USB сохраняется.',networks:'Сети поблизости',networkhint:'Выберите сеть или укажите имя скрытой сети.',refresh:'Обновить',scanwarning:'Обновление списка может ненадолго отключить точку доступа. После сканирования подключитесь к microstick снова.',security:'Защита',open:'Открытая сеть',wifipassword:'Пароль Wi-Fi',switchwarning:'На время подключения точка доступа отключится. Подключитесь к тому же роутеру и откройте новый IP стика. При ошибке точка доступа вернётся автоматически.',connect:'Подключиться',forget:'Забыть сохранённую сеть',simstatus:'SIM И СЕТЬ',pin:'PIN-код SIM',pinhint:'Отправляется один раз и не сохраняется. Проверьте код, чтобы не потратить оставшиеся попытки.',unlock:'Разблокировать SIM',operatorconfig:'Настройки оператора',apnhint:'Стик подбирает настройки по SIM-карте. При необходимости выберите оператора из списка или ручной режим.',advanced:'Авторизация',username:'Имя пользователя',password:'Пароль',auth:'Способ авторизации',none:'Нет',saveconnect:'Сохранить и подключить',disconnect:'Отключить',hotspot:'Ваша точка доступа',aphint:'Изменение настроек отключит подключённых клиентов.',networkname:'Имя сети',newpassword:'Новый пароль',save:'Сохранить',currentpassword:'Текущий пароль',restart:'Перезагрузить',restarthint:'Соединения ненадолго прервутся.',waiting:'Ожидание настройки',online:'Сеть подключена',connecting:'Подключение…',offline:'Нет подключения',missing:'SIM не установлена',locked:'Требуется PIN',puk:'Требуется PUK',failed:'Ошибка подключения',searching:'Поиск сети',registered:'Зарегистрирован в сети',unknown:'Ожидание модема',ready:'Готово',busy:'Другая операция ещё выполняется',connection_failed:'Не удалось подключиться. Проверьте пароль и доступность сети.',connection_lost:'Соединение с Wi-Fi потеряно. Точка доступа восстановлена.',password_error:'Проверьте пароль',wait:'Повторите попытку через несколько секунд',lost:'Связь с устройством прервана. Если меняли сеть, подключитесь к ней снова или найдите новый IP у роутера.',empty:'Список пока пуст. Нажмите «Обновить» или введите SSID вручную.',unsupported:'Не поддерживается',confirm:'Соединение прервётся. Продолжить?',saved:'Настройки сохранены',operation:'Запрос принят. Выполняется…',invalid:'Проверьте введённые данные',core_unavailable:'Сетевой сервис временно недоступен',modem_unavailable:'Модем пока недоступен',pin_failed:'SIM отклонила PIN. Проверьте оставшиеся попытки.',invalid_password:'Пароль Wi-Fi должен содержать от 8 до 63 символов',invalid_ssid:'Имя сети должно занимать от 1 до 32 байт',invalid_lte_parameters:'Параметры оператора содержат неподдерживаемые символы',pin_accepted:'SIM разблокирована',rebooting:'Устройство перезагружается',scan_complete:'Список сетей обновлён',connected:'Подключено',forgotten:'Сеть удалена',mode_saved:'Режим сохранён',ap_saved:'Настройки точки доступа сохранены',lte_saved:'Настройки мобильной сети сохранены'};
const enExtra={disabled:'Mobile network disabled',enabled:'Ready to register',enabling:'Starting modem',disabling:'Stopping modem',disconnecting:'Disconnecting',login:'Sign in again',waiting:'Ready to set up',online:'Network connected',connecting:'Connecting…',offline:'Not connected',missing:'No SIM inserted',locked:'SIM PIN required',puk:'SIM PUK required',failed:'Connection failed',searching:'Searching for network',registered:'Registered',unknown:'Waiting for modem',ready:'Done',busy:'Another operation is in progress',connection_failed:'Could not connect. Check the password and network availability.',connection_lost:'Wi-Fi disconnected. The hotspot has been restored.',password_error:'Check your password',wait:'Please retry in a few seconds',lost:'Connection to the device was interrupted. Rejoin the network or find its new IP in your router.',empty:'No networks yet. Refresh or enter an SSID manually.',unsupported:'Unsupported',confirm:'The connection will be interrupted. Continue?',saved:'Settings saved',operation:'Request accepted. Working…',invalid:'Check the entered values',core_unavailable:'Network service temporarily unavailable',pin_failed:'SIM rejected the PIN. Check remaining attempts.',invalid_password:'Wi-Fi password must be 8–63 characters',invalid_ssid:'Network name must be 1–32 bytes',modem_unavailable:'Modem unavailable'};
Object.assign(ru, {
  modem_not_ready:'Радиомодем не готов (QMI 52)',modem_not_ready_hint:'Прошивка модема отклонила включение радиосети: DeviceNotReady. Ошибка возникает до регистрации у оператора. Если полное отключение питания не помогло, нужна диагностика запуска модема. Ввод PIN и смена APN не устранят этот отказ.',
  cancel:'Отмена',continue:'Продолжить',confirmtitle:'Подтвердите действие',
  confirm_scan:'Поиск сетей ненадолго отключит точку доступа. Начать поиск?',
  confirm_forget:'Удалить сохранённую сеть Wi-Fi? Стик вернётся в режим точки доступа.',
  confirm_reboot:'Перезагрузить microstick? Интернет и панель будут временно недоступны.',
  confirm_ap:'Сохранить настройки точки доступа? Подключённые клиенты потеряют связь.',
  confirm_mode:'Изменить источник интернета? Wi-Fi может отключиться. Доступ по USB сохранится.',
  confirm_connect:'Подключиться к этой сети Wi-Fi? Точка доступа отключится. При ошибке она восстановится.',
  mobileaction:'Открыть настройки',retrylte:'Повторить подключение',enterpin:'Ввести PIN',
  lte_disabled:'Мобильная сеть отключена настройкой',lte_disabled_hint:'Нажмите «Сохранить и подключить», чтобы включить мобильный интернет.',
  lte_wifi_hint:'Выбран интернет через Wi-Fi. Для мобильной сети выберите мобильную сеть или автоматический режим в обзоре.',
  modem_starting:'Запуск мобильной сети',modem_starting_hint:'Контроллер включает модем и подключается к оператору. Дождитесь результата.',
  sim_pin_required:'SIM требует PIN-код',sim_pin_required_hint:'Введите PIN этой SIM в разделе «Мобильная сеть». Код не сохраняется и не отправляется повторно автоматически.',
  sim_puk_required:'SIM заблокирована: требуется PUK',sim_puk_required_hint:'Попытки PIN исчерпаны. Получите PUK у оператора и разблокируйте SIM в телефоне, затем верните её в стик.',
  sim_locked:'SIM заблокирована',sim_locked_hint:'Требуется код блокировки оператора или SIM. Уточните его у оператора; обычный PIN здесь не поможет.',
  sim_missing:'SIM не обнаружена',sim_missing_hint:'Выключите стик, проверьте установку SIM и подключите питание снова.',
  apn_required:'Нужно указать APN',apn_required_hint:'Не удалось однозначно выбрать профиль. Введите APN и, если нужны, имя пользователя и пароль из настроек оператора.',
  registration_denied:'Оператор отказал в регистрации',registration_denied_hint:'Проверьте активацию SIM, покрытие и разрешение роуминга у оператора.',
  lte_no_network:'Нет сети оператора',lte_no_network_hint:'Проверьте покрытие и положение стика, затем повторите подключение.',
  lte_timeout:'Время подключения истекло',lte_timeout_hint:'Проверьте сигнал и настройки оператора. Контроллер повторит попытку; можно повторить сейчас.',
  lte_connect_failed:'Не удалось подключить мобильный интернет',lte_connect_failed_hint:'Проверьте APN, авторизацию, баланс и ограничения тарифа. Следующая попытка выполняется автоматически.',
  modem_failed:'Ошибка модема или SIM',modem_failed_hint:'Перезагрузите стик. Если ошибка остаётся, проверьте SIM в телефоне.',
  modem_unavailable_hint:'Стик ожидает модем. Если состояние не меняется, перезагрузите устройство.',
  lte_ip_failed:'Не удалось настроить адрес мобильной сети',lte_ip_failed_hint:'Адрес оператора некорректен или пересекается с локальной сетью. Проверьте настройки сети.',
  sim_code_in_config:'PIN нельзя хранить в настройках мобильной сети',sim_code_in_config_hint:'Удалите PIN/PUK из сохранённых параметров APN. Вводите PIN только в отдельной форме.',
  modem_busy:'Модем выполняет другую операцию',modem_busy_hint:'Дождитесь окончания текущей попытки подключения.',
  lte_registered_hint:'SIM зарегистрирована. Ожидается подключение к интернету.',
  lte_connected_hint:'Мобильный интернет подключён.',lte_searching_hint:'Модем ищет сеть оператора. Проверьте покрытие, если поиск затянулся.',
  pin2note:'SIM сообщает PIN2/PUK2 для дополнительных функций. Для мобильного интернета этот код не требуется.',
  retries:'Осталось попыток PIN: ',retries_unknown:'Число оставшихся попыток PIN неизвестно. Проверьте код у оператора.',
  invalid_pin:'PIN должен содержать от 4 до 8 цифр',pin_not_required:'Обычный PIN сейчас не требуется. Обновите состояние SIM.',
  lte_saved:'Настройки сохранены. Состояние подключения показано в разделе «Мобильная сеть».',
});
Object.assign(enExtra, {
  modem_not_ready:'Radio modem not ready (QMI 52)',modem_not_ready_hint:'Modem firmware refused to enable the radio: DeviceNotReady. This happens before operator registration. If disconnecting power completely does not help, modem startup needs investigation. PIN and APN changes will not fix this failure.',
  cancel:'Cancel',continue:'Continue',confirmtitle:'Confirm action',
  confirm_scan:'Scanning briefly disconnects the hotspot. Start scanning?',
  confirm_forget:'Delete the saved Wi-Fi network? The stick will return to hotspot mode.',
  confirm_reboot:'Restart microstick? Internet and panel access will be temporarily unavailable.',
  confirm_ap:'Save hotspot settings? Connected clients will lose their connection.',
  confirm_mode:'Change the internet source? Wi-Fi may disconnect. USB access remains available.',
  confirm_connect:'Connect to this Wi-Fi network? The hotspot will stop and return if the connection fails.',
  mobileaction:'Open settings',retrylte:'Retry connection',enterpin:'Enter PIN',
  lte_disabled:'Mobile network disabled in settings',lte_disabled_hint:'Select “Save & connect” to enable mobile internet.',
  lte_wifi_hint:'Wi-Fi internet is selected. Choose Mobile or Automatic in Overview to use mobile data.',
  modem_starting:'Starting mobile connection',modem_starting_hint:'The controller is enabling the modem and connecting to your operator. Please wait.',
  sim_pin_required:'SIM PIN required',sim_pin_required_hint:'Enter this SIM’s PIN in Mobile network. It is never saved or retried automatically.',
  sim_puk_required:'SIM blocked: PUK required',sim_puk_required_hint:'PIN attempts are exhausted. Obtain the PUK from your operator and unlock the SIM in a phone before reinserting it.',
  sim_locked:'SIM locked',sim_locked_hint:'An operator or SIM lock code is required. Contact your operator; a regular SIM PIN will not unlock it.',
  sim_missing:'SIM not detected',sim_missing_hint:'Power off the stick, check the SIM installation, then reconnect power.',
  apn_required:'APN required',apn_required_hint:'No unique operator profile was found. Enter the APN and any credentials supplied by your operator.',
  registration_denied:'Network registration denied',registration_denied_hint:'Check SIM activation, coverage and roaming permissions with your operator.',
  lte_no_network:'No operator network',lte_no_network_hint:'Check coverage and reposition the stick, then retry.',
  lte_timeout:'Connection timed out',lte_timeout_hint:'Check signal and operator settings. The controller retries automatically; you can also retry now.',
  lte_connect_failed:'Mobile internet connection failed',lte_connect_failed_hint:'Check APN, authentication, balance and plan restrictions. Another attempt is made automatically.',
  modem_failed:'Modem or SIM error',modem_failed_hint:'Restart the stick. If the error persists, test the SIM in a phone.',
  modem_unavailable_hint:'Waiting for the modem. Restart the device if this state persists.',
  lte_ip_failed:'Could not configure the mobile address',lte_ip_failed_hint:'The operator address is invalid or overlaps the local network. Check network settings.',
  sim_code_in_config:'Remove PIN from mobile settings',sim_code_in_config_hint:'Remove PIN/PUK from saved APN parameters. Use the separate PIN form.',
  modem_busy:'Modem busy',modem_busy_hint:'Wait for the current connection attempt to finish.',
  lte_registered_hint:'SIM registered. Waiting for an internet connection.',lte_connected_hint:'Mobile internet connected.',
  lte_searching_hint:'Searching for your operator’s network. Check coverage if this takes too long.',
  pin2note:'The SIM reports PIN2/PUK2 for supplementary features. This code is not required for mobile internet.',
  retries:'PIN attempts remaining: ',retries_unknown:'Remaining PIN attempts are unknown. Verify the code with your operator.',
  invalid_pin:'PIN must contain 4–8 digits',pin_not_required:'A regular PIN is not currently required. Refresh the SIM status.',
  pin_accepted:'SIM unlocked',rebooting:'Device restarting',scan_complete:'Networks refreshed',connected:'Connected',forgotten:'Network forgotten',mode_saved:'Mode saved',ap_saved:'Hotspot settings saved',
  lte_saved:'Settings saved. See Mobile network for connection progress.',
});
Object.assign(ru, {
  ttlhint:'Задаёт TTL исходящих IPv4-пакетов мобильной сети, включая трафик клиентов USB и Wi-Fi.',ttlmode:'Fix TTL',on:'Включён',off:'Выключен',ttlvalue:'TTL на выходе',ttl_active:'Работает, TTL: ',ttl_pending:'Ожидание применения TTL',invalid_ttl:'TTL должен быть целым числом от 1 до 255',ttl_apply_failed:'Не удалось применить Fix TTL',ttl_saved:'Fix TTL сохранён и применён',
  imeihint:'Укажите IMEI из 15 цифр. Стик перезагрузится и применит его до подключения к оператору.',imeivalue:'IMEI',saveandreboot:'Сохранить и перезагрузить',imeirestore:'Вернуть IMEI по умолчанию',imei_default:'IMEI прошивки по умолчанию: ',imei_current:'Текущий IMEI: ',imei_pending:'Ожидание применения IMEI',invalid_imei:'Нужны 15 цифр IMEI с корректной контрольной цифрой',imei_saved_reboot:'IMEI сохранён. Устройство перезагружается.',confirm_imei:'Сохранить этот IMEI и перезагрузить стик? Интернет и панель будут временно недоступны.',confirm_imeirestore:'Вернуть IMEI, заданный в прошивке по умолчанию, и перезагрузить стик?',
  imei_busy:'DIAG занят, повторите попытку',imei_unavailable:'Ожидание DIAG модема',imei_timeout:'Модем не ответил на запрос IMEI',imei_locked:'Модем запретил запись IMEI',imei_nv_rejected:'Модем отклонил IMEI',imei_verify_failed:'Проверка записанного IMEI не прошла',imei_io_failed:'Ошибка связи с DIAG',imei_invalid_response:'Некорректный ответ DIAG',imei_apply_failed:'Не удалось применить IMEI',
});
Object.assign(enExtra, {
  ttl_active:'Active, TTL: ',ttl_pending:'Waiting to apply TTL',invalid_ttl:'TTL must be an integer from 1 to 255',ttl_apply_failed:'Could not apply Fix TTL',ttl_saved:'Fix TTL saved and applied',
  imei_default:'Firmware default IMEI: ',imei_current:'Current IMEI: ',imei_pending:'Waiting to apply IMEI',invalid_imei:'Enter 15 IMEI digits with a valid check digit',imei_saved_reboot:'IMEI saved. Device restarting.',confirm_imei:'Save this IMEI and restart the stick? Internet and panel access will be temporarily unavailable.',confirm_imeirestore:'Restore the firmware default IMEI and restart the stick?',
  imei_busy:'DIAG busy, please retry',imei_unavailable:'Waiting for modem DIAG',imei_timeout:'IMEI request timed out',imei_locked:'Modem denied IMEI write',imei_nv_rejected:'Modem rejected the IMEI',imei_verify_failed:'IMEI readback verification failed',imei_io_failed:'DIAG connection error',imei_invalid_response:'Invalid DIAG response',imei_apply_failed:'Could not apply IMEI',
});
const en={...enExtra};document.querySelectorAll('[data-t]').forEach(e=>en[e.dataset.t]=e.textContent);
let dialogTrigger=null;
let lang=localStorage.getItem('language')||(/^ru/i.test(navigator.language)?'ru':'en'),csrf='',latest={},lastNetworks='',feedbackTimer;
const t=k=>(lang==='ru'?ru[k]:en[k])||en[k]||k.replaceAll('_',' ');
function language(){document.documentElement.lang=lang;document.querySelectorAll('[data-t]').forEach(e=>e.textContent=t(e.dataset.t));for(const id of ['lang','loginlang'])$(id).textContent=lang==='ru'?'EN':'RU';if(typeof currentPage!=='undefined')$('headline').textContent=t(currentPage);document.querySelectorAll('[data-t-title]').forEach(e=>e.title=t(e.dataset.tTitle));document.querySelectorAll('.reveal').forEach(b=>b.setAttribute('aria-label',lang==='ru'?'Показать или скрыть пароль':'Show or hide password'));lastNetworks='';if(latest.wifi)render(latest);}
function feedback(message,error=false,hold=false){clearTimeout(feedbackTimer);$('feedback').textContent=message;$('feedback').hidden=false;$('feedback').classList.toggle('error',error);if(!hold)feedbackTimer=setTimeout(()=>$('feedback').hidden=true,9000);}
async function api(path,data){const options={cache:'no-store',credentials:'same-origin',signal:AbortSignal.timeout(15000)};if(data!==undefined){options.method='POST';options.headers={'Content-Type':'application/json','X-Microstick-CSRF':csrf};options.body=JSON.stringify(data);}let response;try{response=await fetch('/api/'+path,options);}catch(e){throw new Error('lost');}let result;try{result=await response.json();}catch(e){throw new Error('invalid');}if(response.status===401&&path!=='login'){showLogin();throw new Error('login');}if(!response.ok)throw new Error(result.error==='password'?'password_error':result.error||'invalid');return result;}
function showLogin(){closeMenu();document.body.classList.remove('authenticated');if($('confirmation').open)$('confirmation').close('cancel');$('app').hidden=true;$('login').hidden=false;$('logout').hidden=true;csrf='';}
function authenticated(){document.body.classList.add('authenticated');$('login').hidden=true;$('app').hidden=false;$('logout').hidden=false;refresh(true);if(typeof refreshLan==='function')refreshLan();}
function values(form){return Object.fromEntries(new FormData(form));}
function askConfirmation(name){
  return new Promise(resolve=>{
    const dialog=$('confirmation');
    $('confirmtext').textContent=t('confirm_'+name);
    dialog.returnValue='cancel';
    const trigger=dialogTrigger||document.activeElement;
    dialog.addEventListener('close',()=>{resolve(dialog.returnValue==='continue');requestAnimationFrame(()=>{if(trigger?.isConnected&&!trigger.disabled)trigger.focus({preventScroll:true});});},{once:true});
    dialog.showModal();
  });
}
async function operation(name,data,warning=false){if(warning&&!await askConfirmation(name))return;const result=await api(name,data);if(result.operation){localStorage.setItem('operation',result.operation);feedback(t('operation'),false,true);}else feedback(t('saved'));}
function lteView(s){
  const g=s.modem?.modem?.generic||{},gsm=s.modem?.modem?.['3gpp']||{},lock=g['unlock-required'];
  let code='',hint='',problem=false;
  if(g['state-failed-reason']==='sim-missing')code='sim_missing';
  else if(lock==='sim-pin')code='sim_pin_required';
  else if(lock==='sim-puk')code='sim_puk_required';
  else if(lock&&!['none','--','unknown','sim-pin2','sim-puk2'].includes(lock))code='sim_locked';
  else if(g.state==='failed'||(g.state==='locked'&&!['sim-pin2','sim-puk2'].includes(lock)))code='modem_failed';
  if(code)problem=true;
  else if(!s.lte_enabled){code='lte_disabled';hint=s.policy==='wifi'?'lte_wifi_hint':'lte_disabled_hint';}
  else if(g.state==='connected'){code='connected';hint=s.uplink?.startsWith('wwan')?'lte_connected_hint':'mobile_standby_hint';}
  else if(gsm['registration-state']==='denied'){code='registration_denied';problem=true;}
  else if(s.lte?.connecting){code='modem_starting';}
  else if(s.lte?.error){code=s.lte.error;problem=true;}
  else if(g.state==='searching'){code='searching';hint='lte_searching_hint';}
  else if(g.state==='registered'||g.state==='connected'||['home','roaming'].includes(gsm['registration-state'])){code='registered';hint='lte_registered_hint';}
  else if(!g.state){code='modem_unavailable';problem=true;}
  else if((g.state==='unknown'||g.state==='enabled')&&Number(g['signal-quality']?.value)>0){code='network_waiting';hint='network_waiting_hint';}
  else{code='modem_starting';}
  const retry=(g['unlock-retries']||[]).find?.(v=>typeof v==='string'&&/^sim-pin \(/.test(v));
  const remaining=retry?Number(retry.match(/\((\d+)\)/)?.[1]):null;
  return {code,hint:hint||code+'_hint',problem,lock,remaining};
}
let wifiConfirmBusy=false,wifiConfirmed='';
function confirmWifi(s){const p=s.wifi_connection;if(!p?.pending||!p.id||!csrf||wifiConfirmBusy||wifiConfirmed===p.id)return;wifiConfirmBusy=true;api('wifi_confirm',{id:p.id}).then(()=>{wifiConfirmed=p.id;}).catch(()=>{}).finally(()=>{wifiConfirmBusy=false;});}
function render(s){latest=s;confirmWifi(s);const modem=s.modem?.modem,g=modem?.generic||{},gsm=modem?.['3gpp']||{},v=lteView(s);
const ttl=s.ttl||{},imei=s.imei||{};
$('ttlstate').textContent=ttl.error?t(ttl.error):ttl.applied?(ttl.enabled?t('ttl_active')+ttl.value:t('off')):t('ttl_pending');

$('imeistate').textContent=imei.error?t(imei.error):(imei.effective||imei.configured)&&imei.current!==(imei.effective||imei.configured)?t('imei_pending')+ (imei.current?' · '+t('imei_current')+imei.current:''):t('imei_current')+(imei.current||'—');
renderConnection(s);renderLteProfile(s.lte);
if(document.activeElement!==$('mode'))$('mode').value=s.policy||'auto';
const key=JSON.stringify(s.networks||[]);if(key!==lastNetworks){lastNetworks=key;$('networks').replaceChildren();if(!(s.networks||[]).length){const p=document.createElement('p');p.className='muted';p.textContent=t('empty');$('networks').append(p);}for(const n of s.networks||[]){if(!n.ssid)continue;const button=document.createElement('button');button.type='button';button.className='network';const title=document.createElement('span');title.textContent=n.ssid;const meta=document.createElement('small');meta.textContent=Math.max(0,Math.min(100,Math.round((n.signal+100)*2)))+'% · '+(n.security==='open'?t('open'):n.security==='wpa2'?'WPA2':t('unsupported'));button.append(title,meta);button.disabled=n.security==='unsupported';button.onclick=()=>{$('ssid').value=n.ssid;$('ssid').dataset.hex=n.ssid_hex||'';$('security').value=n.security;$('wifipassword').required=n.security!=='open';$('wifipassword').focus();};$('networks').append(button);}}
if(s.error&&localStorage.getItem('shownerror')!==s.error){feedback(t(s.error),true);localStorage.setItem('shownerror',s.error);}if(!s.error)localStorage.removeItem('shownerror');}
let refreshing=false;async function refresh(config=false){if(!csrf||refreshing)return;refreshing=true;try{const s=await api('status');render(s);if(config){const c=await api('config');if(document.activeElement!==$('hostname'))$('hostname').value=c.device?.hostname||'microstick';$('apssid').value=c.ssid||'';window.panelSSID=c.ssid||'';configureLteProfile(c);$('ttlenabled').value=String(c.ttl?.enabled!==false);$('ttlvalue').value=c.ttl?.value||64;$('imeivalue').value=c.imei?.effective||c.imei?.configured||c.imei?.default||s.imei?.current||'';}if(localStorage.getItem('operation')){const op=await api('operation');if(!op.busy&&(!op.id||op.id===localStorage.getItem('operation'))){localStorage.removeItem('operation');setTimeout(()=>refresh(true),0);feedback(t(op.result?.error||op.result?.message||'ready'),op.result?.ok===false);}}}catch(e){if(e.message!=='login')feedback(t(e.message),true);}finally{refreshing=false;}}
$('lang').onclick=$('loginlang').onclick=()=>{lang=lang==='ru'?'en':'ru';localStorage.setItem('language',lang);language();};
function selectTab(tab){navigatePanel(tab);}
document.querySelectorAll('[data-tab]').forEach(b=>b.onclick=()=>selectTab(b.dataset.tab));
$('mobileaction').onclick=()=>{selectSubTab('lte','connection');if(!$('pinform').hidden)$('pin').focus();else $('apn').focus();};
document.querySelectorAll('.reveal').forEach(b=>b.onclick=()=>{const input=b.previousElementSibling;input.type=input.type==='password'?'text':'password';});
function form(id,action){$(id).onsubmit=async event=>{event.preventDefault();const button=event.submitter;dialogTrigger=button;if(button)button.disabled=true;try{await action(values($(id)));}catch(e){feedback(t(e.message),true);}finally{if(button)button.disabled=false;}};}
form('loginform',async data=>{const r=await api('login',{password:data.password,remember:$('rememberlogin').checked});csrf=r.csrf;$('loginpassword').value='';authenticated();});
$('ssid').addEventListener('input',()=>{delete $('ssid').dataset.hex;});
form('connectform',async data=>{if($('ssid').dataset.hex)data.ssid_hex=$('ssid').dataset.hex;else if(new TextEncoder().encode(data.ssid).length>32)throw new Error('invalid_ssid');await operation('connect',data,true);$('wifipassword').value='';});
form('hostnameform',data=>operation('device',data));
form('modeform',data=>operation('mode',data,true));form('apform',data=>operation('ap',data,true));form('lteform',saveLteProfile);form('pinform',async data=>{await operation('pin',data);$('pin').value='';});form('passwordform',async data=>{await api('password',data);$('passwordform').reset();showLogin();feedback(t('password_changed'));});
function button(id,fn){$(id).onclick=async()=>{const b=$(id);dialogTrigger=b;b.disabled=true;try{await fn();}catch(e){feedback(t(e.message),true);}finally{b.disabled=false;}};}
function validIMEI(imei){return /^[0-9]{15}$/.test(imei)&&imei!=='000000000000000'&&[...imei].reduce((sum,c,i)=>{let n=Number(c);if(i%2){n*=2;if(n>9)n-=9;}return sum+n;},0)%10===0;}
form('ttlform',data=>{if(!/^[0-9]+$/.test(data.value)||Number(data.value)<1||Number(data.value)>255)throw new Error('invalid_ttl');return operation('ttl',{...data,enabled:data.enabled==='true'});});
form('imeiform',data=>{if(!validIMEI(data.imei))throw new Error('invalid_imei');return operation('imei',data,true);});
button('imeirestore',async()=>{if(await askConfirmation('imeirestore'))await operation('imei',{restore:true});});
button('lteretry',()=>operation('lte',{enabled:true}));
button('scan',()=>operation('scan',{},true));button('forget',()=>operation('forget',{},true));button('ltedisconnect',()=>operation('lte',{enabled:false}));button('reboot',()=>operation('reboot',{},true));button('logout',async()=>{await api('logout',{});showLogin();});
$('security').onchange=()=>{$('wifipassword').required=$('security').value!=='open';$('wifipassword').disabled=$('security').value==='open';};
Object.assign(ru,{
 wifi_verifying:'Проверка подключения Wi-Fi',mobile_standby_hint:'Мобильные данные подключены. Выбранный источник интернета показан в обзоре.',remember_login:'Запомнить вход на 30 дней',password_changed:'Пароль изменён. Войдите с новым паролем.',storage:'Не удалось сохранить данные',
 device_name:'Имя устройства',local_name:'Имя в локальной сети',invalid_hostname:'Используйте до 63 латинских строчных букв, цифр и дефисов; дефис не может быть крайним.',hostname_saved:'Имя сохранено. Откройте панель по новому имени.',mdns_waiting:'Ожидание публикации имени',
 radio_unknown:'Тип сети не определён',network_waiting:'Сигнал есть, регистрация не подтверждена',network_waiting_hint:'Ожидаем регистрацию у оператора. Уровень сигнала сам по себе не означает доступ к передаче данных.',
 wifi_pending:'Подключение проверяется. Подключитесь к выбранной сети и откройте панель за',seconds:'с',captive_portal:'Сеть требует авторизации или подменяет проверку доступа. Точка доступа восстановлена.',no_internet:'Доступ к интернету через Wi-Fi не подтверждён. Точка доступа восстановлена.',wifi_access_unconfirmed:'Панель в новой сети не была доступна или не открывалась. Точка доступа восстановлена.',wifi_save_failed:'Не удалось сохранить Wi-Fi. Точка доступа восстановлена.',invalid_wifi_parameters:'Проверьте имя, защиту и пароль Wi-Fi',wifi_confirm_interface:'Откройте панель через выбранную сеть Wi-Fi',
 addresshint:'Откройте панель по имени .local или текущему IP. Если роутер изолирует клиентов, используйте USB или дождитесь возврата точки доступа.',switchwarning:'Точка доступа временно отключится. Подключитесь к выбранной сети и откройте панель по её имени .local. Если панель не откроется за 120 секунд после выдачи IP, точка доступа вернётся автоматически.',
 modem_ports:'Порты модема',ports_hint:'Разрешите доступ из подключённой сети Wi-Fi к программам на модеме. Программа должна быть запущена и слушать этот порт. USB и собственная точка доступа уже разрешают локальные подключения.',add_port:'Добавить порт',port_source:'IP или подсеть источника (необязательно)',port_source_hint:'Пустое поле: только текущая подсеть подключённого Wi-Fi.',no_ports:'Дополнительные порты не открыты',
 wifi_history:'История Wi-Fi',wifi_saved_history:'Сохранённая статистика Wi-Fi',save_wifi_history:'Сохранять историю использования Wi-Fi',wifi_persistence_hint:'Отдельно от мобильной сети. При выключении запись приостанавливается, архив сохраняется. Новое включение считает только последующий трафик.',confirm_wifi_statistics:'Сбросить текущий период Wi-Fi? История по дням и месяцам сохранится.',wifi_statistics_invalid:'Файлы истории Wi-Fi повреждены; перезапись остановлена.',wifi_statistics_reset:'Начат новый период Wi-Fi'
});
Object.assign(en,{
 wifi_verifying:'Verifying Wi-Fi connection',mobile_standby_hint:'Mobile data is connected. The selected internet source is shown in Overview.',remember_login:'Remember me for 30 days',password_changed:'Password changed. Sign in with the new password.',storage:'Could not save data',
 device_name:'Device name',local_name:'Local name',invalid_hostname:'Use up to 63 lowercase letters, digits and internal hyphens.',hostname_saved:'Name saved. Open the panel at its new name.',mdns_waiting:'Waiting for name publication',
 radio_unknown:'Network type unavailable',network_waiting:'Signal detected; registration not confirmed',network_waiting_hint:'Waiting for operator registration. Signal strength alone does not mean mobile data is available.',
 wifi_pending:'Verifying connection. Join the selected network and open the panel within',seconds:'s',captive_portal:'The network requires sign-in or intercepts the connectivity check. Hotspot restored.',no_internet:'Internet access over Wi-Fi could not be verified. Hotspot restored.',wifi_access_unconfirmed:'The panel was unreachable or was not opened in the new network. Hotspot restored.',wifi_save_failed:'Could not save Wi-Fi. Hotspot restored.',invalid_wifi_parameters:'Check the Wi-Fi name, security and password',wifi_confirm_interface:'Open the panel through the selected Wi-Fi network',
 addresshint:'Open the panel using its .local name or current IP. If the router isolates clients, use USB or wait for the hotspot to return.',switchwarning:'The hotspot will stop temporarily. Join the selected network and open the panel using its .local name. If the panel does not load within 120 seconds after an IP is assigned, the hotspot returns automatically.',
 modem_ports:'Modem ports',ports_hint:'Allow access from the connected Wi-Fi network to programs on the modem. A program must be running and listening on that port. USB and the modem hotspot already permit local connections.',add_port:'Add port',port_source:'Source IP or subnet (optional)',port_source_hint:'Empty: only the current connected Wi-Fi subnet.',no_ports:'No additional ports are open',
 wifi_history:'Wi-Fi history',wifi_saved_history:'Saved Wi-Fi usage',save_wifi_history:'Save Wi-Fi usage history',wifi_persistence_hint:'Separate from mobile traffic. Disabling pauses recording and keeps the archive. Enabling counts subsequent traffic only.',confirm_wifi_statistics:'Reset the current Wi-Fi period? Daily and monthly history will be kept.',wifi_statistics_invalid:'Wi-Fi snapshots are damaged; overwriting is disabled.',wifi_statistics_reset:'New Wi-Fi period started'
});
Object.assign(ru,{lte_profile:'Профиль оператора',lte_manual:'Вручную',lte_detecting:'Определяем профиль по SIM-карте…',lte_select_operator:'Выберите оператора из списка или укажите собственный APN.',apn_pending:'Определяем профиль оператора',apn_pending_hint:'Ожидаем данные SIM-карты. Настройки будут выбраны автоматически.',invalid_operator_profile:'Выберите доступный профиль оператора',apn_required_hint:'Не удалось однозначно определить оператора. Выберите его из списка готовых профилей. Для частной сети можно указать APN вручную.'});
Object.assign(en,{lte_profile:'Operator profile',lte_manual:'Manual',lte_detecting:'Detecting the SIM profile…',lte_select_operator:'Select an operator from the list or enter a custom APN.',apn_pending:'Detecting operator profile',apn_pending_hint:'Waiting for SIM details. Settings will be selected automatically.',invalid_operator_profile:'Select an available operator profile',apn_required_hint:'The operator could not be identified uniquely. Select a ready-made operator profile, or enter a private-network APN manually.'});
initLteProfiles();language();api('session').then(r=>{csrf=r.csrf;authenticated();}).catch(()=>showLogin());setInterval(()=>{if(!document.hidden)refresh();},3000);document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
