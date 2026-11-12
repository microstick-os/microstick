'use strict';
let lteDraftDirty=false;
const lteTouched=new Set();
let lteProfiles=[];

function lteFields(){
 const manual=$('lteprofile').value==='manual';
 $('apn').readOnly=!manual;
 $('apn').required=manual;
 $('lteauthfields').hidden=!manual;
 for(const id of ['username','ltepassword','auth'])$(id).disabled=!manual;
 if(!manual&&$('lteprofile').value!=='auto')$('apn').value=lteProfiles.find(p=>p.id===$('lteprofile').value)?.apn||'';
}
function configureLteProfile(c){
 const lte=c.lte||{profile:c.apn?'manual':'auto',effective_apn:c.apn||''};
 lteProfiles=lte.profiles||[];
 const chosen=lteDraftDirty?$('lteprofile').value:lte.profile||'auto';
 const select=$('lteprofile');select.replaceChildren();
 for(const p of [{id:'auto',name:t('auto')},...lteProfiles,{id:'manual',name:t('lte_manual')}]){
  const option=document.createElement('option');option.value=p.id;option.textContent=p.name;
  if(p.id==='auto'||p.id==='manual')option.dataset.t=p.id==='auto'?'auto':'lte_manual';
  select.append(option);
 }
 // Retain a saved profile even when inspecting an older image/catalog.
 if(![...select.options].some(o=>o.value===chosen)){const option=document.createElement('option');option.value=chosen;option.textContent=lte.provider||chosen;select.append(option);}
 select.value=chosen;
 if(!lteDraftDirty){$('apn').value=lte.effective_apn||c.apn||'';if(lte.auth)$('auth').value=lte.auth;}
 lteFields();renderLteProfile(lte);
}
function renderLteProfile(lte){
 if(!lte)return;
 if(!lteDraftDirty&&lte.profile&&[...$('lteprofile').options].some(o=>o.value===lte.profile))$('lteprofile').value=lte.profile;
 if(!lteDraftDirty&&lte.effective_apn!==undefined)$('apn').value=lte.effective_apn;
 const parts=[];
 if(lte.provider)parts.push(lte.provider);
 if(lte.effective_apn)parts.push('APN: '+lte.effective_apn);
 if(!parts.length)parts.push(t(lte.state==='unknown'||lte.state==='ambiguous'?'lte_select_operator':'lte_detecting'));
 $('lteprofileinfo').textContent=parts.join(' · ');
 lteFields();
}
function initLteProfiles(){
 $('lteprofile').onchange=()=>{lteDraftDirty=true;lteFields();if($('lteprofile').value==='auto')$('apn').value='';};
 for(const id of ['apn','username','ltepassword','auth']){
  $(id).addEventListener('input',()=>{lteDraftDirty=true;lteTouched.add(id);});
  $(id).addEventListener('change',()=>{lteDraftDirty=true;lteTouched.add(id);});
 }
}
async function saveLteProfile(data){
 const request={profile:$('lteprofile').value,enabled:true};
 if(request.profile==='manual'){
  request.apn=data.apn;
  if(lteTouched.has('username'))request.username=data.username;
  if(lteTouched.has('ltepassword'))request.password=data.password;
  if(lteTouched.has('auth'))request.auth=data.auth;
 }
 await operation('lte',request);
 lteDraftDirty=false;lteTouched.clear();$('username').value='';$('ltepassword').value='';
 if(!localStorage.getItem('operation'))refresh(true);
}
