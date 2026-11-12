'use strict';
let statisticsState=null,statisticsBusy=false,lastHistory=0,lastHistoryPeriod='',historyData=null;
function volume(value){let n=Number(value||0);const units=lang==='ru'?['Б','КиБ','МиБ','ГиБ','ТиБ']:['B','KiB','MiB','GiB','TiB'];let i=0;while(n>=1024&&i<units.length-1){n/=1024;i++;}return new Intl.NumberFormat(lang,{maximumFractionDigits:i?2:0}).format(n)+' '+units[i];}
function speed(value){return new Intl.NumberFormat(lang,{minimumFractionDigits:2,maximumFractionDigits:2}).format((value||0)*8/1e6)+(lang==='ru'?' Мбит/с':' Mbps');}
function duration(n){n=Math.max(0,Math.floor(n||0));const d=Math.floor(n/86400),h=Math.floor(n%86400/3600),m=Math.floor(n%3600/60);return [d?d+(lang==='ru'?' д':' d'):'',h?h+(lang==='ru'?' ч':' h'):'',m+(lang==='ru'?' мин':' min')].filter(Boolean).join(' ');}
function dateTime(epoch){const local=statisticsState?.lte?.reset_local;if(epoch&&local){const date=new Date(local.replace(' ','T')+':00Z');if(Number.isFinite(date.getTime()))return date.toLocaleString(lang,{dateStyle:'short',timeStyle:'short',timeZone:'UTC'});}return epoch?new Date(epoch*1000).toLocaleString(lang,{dateStyle:'short',timeStyle:'short',timeZone:'UTC'}):t('date_unknown');}
function svgElement(tag,attributes={},text){const e=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [k,v] of Object.entries(attributes))e.setAttribute(k,String(v));if(text!==undefined)e.textContent=text;return e;}
const chartSeries=new Map();
const chartResize=new ResizeObserver(entries=>{for(const {target} of entries){const data=chartSeries.get(target);if(data)drawSeries(target,data);}});
function chartFrame(container,title){container.replaceChildren();const width=container.clientWidth,height=container.clientHeight;const svg=svgElement('svg',{viewBox:`0 0 ${width} ${height}`,role:'img','aria-label':title});svg.append(svgElement('title',{},title));container.append(svg);return svg;}
function drawSeries(container,data){
 if(!chartSeries.has(container))chartResize.observe(container);chartSeries.set(container,data);
 if(!container.clientWidth)return;
 const points=(data?.points||[]).filter(p=>p.length===3&&p.every(Number.isFinite));
 if(points.length<2){container.replaceChildren(node('div',t('no_samples'),'chartempty'));return;}
 const svg=chartFrame(container,(data.source==='lte'?t('mobile_network'):'Wi-Fi')+' · '+t('current_speed'));
 const max=Math.max(125000,...points.flatMap(p=>[p[1],p[2]])),left=56,right=container.clientWidth-12,top=12,bottom=container.clientHeight-28,first=Math.min(-3,points[0][0]);
 for(let i=0;i<4;i++){const y=top+(bottom-top)*i/3;svg.append(svgElement('line',{x1:left,x2:right,y1:y,y2:y,class:'gridline'}));svg.append(svgElement('text',{x:0,y:y+4},speed(max*(1-i/3)).replace(/ (Мбит\/с|Mbps)$/,'')));}
 svg.append(svgElement('text',{x:left,y:container.clientHeight-5},Math.ceil(-first/60)+(lang==='ru'?' мин назад':' min ago')));svg.append(svgElement('text',{x:right,y:container.clientHeight-5,'text-anchor':'end'},lang==='ru'?'Сейчас':'Now'));
 for(let id=1;id<=2;id++){const d=points.map((p,i)=>{const x=left+(right-left)*(p[0]-first)/-first,y=bottom-(bottom-top)*p[id]/max;return (i?'L':'M')+x.toFixed(2)+','+y.toFixed(2);}).join(' ');svg.append(svgElement('path',{d,class:id===1?'rxline':'txline'}));}
}
function historyDate(key){const s=String(key);return s.length===6?s.slice(4)+'/'+s.slice(0,4):s.slice(6)+'.'+s.slice(4,6)+'.'+s.slice(0,4);}
function drawHistory(data,container=$('historytable')){
 const buckets=[...(data?.buckets||[])].sort((a,b)=>a.date-b.date);container.replaceChildren();
 if(!buckets.length){container.append(node('p',t('no_history'),'muted small'));return;}
 const table=node('table'),head=node('thead'),tr=node('tr');for(const k of ['history_date','download','upload'])tr.append(node('th',t(k)));head.append(tr);const tbody=node('tbody');for(const b of buckets.slice().reverse()){const tr=node('tr');tr.append(node('td',historyDate(b.date)),node('td',volume(b.rx)),node('td',volume(b.tx)));tbody.append(tr);}table.append(head,tbody);container.append(table);
}
function renderStatistics(s){
 statisticsState=s;const lte=s.lte||{},wifi=s.wifi||{},sys=s.system||{};
 $('overviewperiod').textContent=volume(BigInt(lte.rx||0)+BigInt(lte.tx||0));$('overviewreset').textContent=dateTime(lte.reset_at);$('overviewuptime').textContent=duration(sys.uptime);
 $('periodstart').textContent=t('since')+dateTime(lte.reset_at);$('periodrx').textContent=volume(lte.rx);$('periodtx').textContent=volume(lte.tx);
 if(document.activeElement!==$('wifipersistence'))$('wifipersistence').checked=!!wifi.persistence;
 $('wifiperiodstart').textContent=wifi.saved?.reset_at?t('since')+dateTime(wifi.saved.reset_at):'';$('wifiperiodrx').textContent=volume(wifi.saved?.rx);$('wifiperiodtx').textContent=volume(wifi.saved?.tx);
 $('wifisessionrx').textContent=volume(wifi.rx);$('wifisessiontx').textContent=volume(wifi.tx);$('wifisessionstate').textContent=wifi.connected?duration(wifi.session_seconds):t('offline');
 $('systemuptime').textContent=duration(sys.uptime);$('systemcpu').textContent=new Intl.NumberFormat(lang,{maximumFractionDigits:1}).format(sys.cpu_percent||0)+'%';$('systemmemory').textContent=volume(sys.memory_used)+' / '+volume(sys.memory_total);$('systemtemperature').textContent=Number.isFinite(sys.modem_temperature)?Math.round(sys.modem_temperature)+' °C':'—';
 $('historytimezone').textContent=t('timezone')+': '+(s.timezone||'—');
 const source=latest.uplink?.startsWith('wwan')?'lte':latest.uplink?'wifi':null;
 const active=source?s[source]:null;$('overviewrx').textContent=active?speed(active.rx_bps):'—';$('overviewtx').textContent=active?speed(active.tx_bps):'—';$('overviewcharttitle').textContent=t('current_speed')+(source?' · '+(source==='lte'?t('mobile_network'):'Wi-Fi'):'');
 document.querySelectorAll('[data-panel^="statistics/"]').forEach(p=>{const source=p.dataset.panel.endsWith('/wifi')?'wifi':'lte',v=s[source]||{};p.querySelector('[data-stat-rx]').textContent=speed(v.rx_bps);p.querySelector('[data-stat-tx]').textContent=speed(v.tx_bps);});
 $('overviewstaterror').hidden=!s.error;$('overviewstaterror').textContent=t(s.error||'');
}
async function refreshStatistics(force=false){
 if(!csrf||statisticsBusy||document.hidden||!['overview','statistics','settings'].includes(currentPage))return;statisticsBusy=true;const view=currentPage+'/'+currentSub;
 try{
  const s=await api('statistics');renderStatistics(s);$('statisticsunavailable').hidden=!s.error;$('statisticsunavailable').textContent=t(s.error||'statistics_unavailable');
  const source=currentPage==='statistics'?currentSub:latest.uplink?.startsWith('wwan')?'lte':latest.uplink?'wifi':null;
  if(source&&['lte','wifi'].includes(source)&&currentPage!=='settings'){
   const series=await api('statistics/series?source='+source),container=currentPage==='overview'?$('overviewchart'):document.querySelector('[data-panel="statistics/'+source+'"] [data-stat-chart]');if(view===currentPage+'/'+currentSub)drawSeries(container,series);
  }else if(currentPage==='overview')$('overviewchart').replaceChildren(node('div',t('offline'),'chartempty'));
  if(currentPage==='statistics'&&currentSub==='lte'){
   const period=$('historyperiod').value;if(force||period!==lastHistoryPeriod||Date.now()-lastHistory>15000){historyData=await api('statistics/history?period='+period);lastHistory=Date.now();lastHistoryPeriod=period;}drawHistory(historyData);
  }
 if(currentPage==='statistics'&&currentSub==='wifi'){wifiHistoryData=await api('statistics/history?source=wifi&period='+$('wifihistoryperiod').value);drawHistory(wifiHistoryData,$('wifihistorytable'));}
 }catch(e){if(e.message==='login')return;$('statisticsunavailable').hidden=false;$('overviewstaterror').hidden=false;$('overviewstaterror').textContent=t(e.message==='lost'?'lost':'statistics_unavailable');$('overviewrx').textContent=$('overviewtx').textContent='—';$('overviewperiod').textContent='—';$('overviewchart').replaceChildren(node('div',t('statistics_unavailable'),'chartempty'));}
 finally{statisticsBusy=false;}
}
$('historyperiod').onchange=()=>refreshStatistics(true);
button('resetstatistics',async()=>{if(!await askConfirmation('statistics'))return;await api('statistics/reset',{});feedback(t('statistics_reset'));await refreshStatistics(true);});
const statisticsLanguage=language;language=function(){statisticsLanguage();if(statisticsState)renderStatistics(statisticsState);if(historyData)drawHistory(historyData);for(const [container,data] of chartSeries)drawSeries(container,data);refreshStatistics(true);};
setInterval(()=>refreshStatistics(),3000);document.addEventListener('visibilitychange',()=>{if(!document.hidden)refreshStatistics();});

let wifiHistoryData=null;
$('wifipersistence').onchange=async()=>{const input=$('wifipersistence');input.disabled=true;try{await api('statistics/config',{wifi_persistence:input.checked});feedback(t('saved'));await refreshStatistics(true);}catch(e){input.checked=!!statisticsState?.wifi?.persistence;feedback(t(e.message),true);}finally{input.disabled=false;}};
$('wifihistoryperiod').onchange=()=>refreshStatistics(true);
button('resetwifistatistics',async()=>{if(!await askConfirmation('wifi_statistics'))return;await api('statistics/reset',{source:'wifi'});feedback(t('wifi_statistics_reset'));await refreshStatistics(true);});
