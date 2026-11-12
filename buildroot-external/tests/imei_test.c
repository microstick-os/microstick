#define main production_main
#include "imei.c"
#undef main

#include <assert.h>
#include <sys/socket.h>
#include <sys/wait.h>
int main(void) {
 uint8_t empty[128]={0},marker[128],foreign[128]={1};
 assert(guard_payload(empty,marker)==1); assert(!memcmp(marker,"HiMI_OK",7));
 uint8_t copy[128];memcpy(copy,marker,128);assert(guard_payload(copy,marker)==0);
 assert(guard_payload(foreign,marker)==-1);assert(foreign[0]==1);
 assert(valid_imei("490154203237518"));
 assert(!valid_imei("490154203237519")); assert(!valid_imei("000000000000000"));
 assert(!valid_imei("49015420323751;reboot"));
 uint8_t encoded[128]; memset(encoded,0xa5,sizeof(encoded));
 encode("490154203237518",encoded); char text[16];
 assert(decode(encoded,text)); assert(!strcmp(text,"490154203237518"));
 for(int i=9;i<128;i++)assert(encoded[i]==0xa5);
 encoded[9]=0x7e;encoded[10]=0x7d;
 int pair[2];assert(!socketpair(AF_UNIX,SOCK_DGRAM,0,pair));
 pid_t child=fork(); assert(child>=0);
 if(!child){
  close(pair[0]); uint8_t req[133];
  assert(read(pair[1],req,133)==133);
  assert(req[0]==0x26&&req[1]==0x26&&req[2]==2);
  memcpy(req+3,encoded,128);req[131]=req[132]=0;
  uint8_t frame[512];size_t n=0;uint16_t check=(uint16_t)~crc(req,133);
  uint8_t raw[135];memcpy(raw,req,133);raw[133]=check&255;raw[134]=check>>8;
  for(size_t i=0;i<135;i++){if(raw[i]==0x7d||raw[i]==0x7e){frame[n++]=0x7d;frame[n++]=raw[i]^0x20;}else frame[n++]=raw[i];}
  frame[n++]=0x7e;assert(write(pair[1],frame,n)==(ssize_t)n);_exit(0);
 }
 close(pair[1]);uint8_t data[128];assert(nv(pair[0],0x26,550,data)==NULL);
 assert(!memcmp(data,encoded,128));int status;waitpid(child,&status,0);assert(WIFEXITED(status)&&!WEXITSTATUS(status));close(pair[0]);
 return 0;
}
