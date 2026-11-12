#define _GNU_SOURCE
#include "model.h"
#include <assert.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
int main(int argc,char **argv){
 assert(argc==2);setenv("TZ","UTC-5",1);tzset();
 struct model a;model_init(&a);a.last_save=1;
 uint64_t c[4]={1000,2000,0,0};
 model_sample(&a,c,"boot1","table1","hotel",1,1793746800,1);
 assert(a.d.rx==1000&&a.d.tx==2000&&a.count==0);
 c[0]+=300;c[1]+=600;c[2]=UINT64_C(42949672960);c[3]=100;
 model_sample(&a,c,"boot1","table1","hotel",4,1793746803,1);
 assert(a.d.rx==1300&&a.d.tx==2600&&a.wifi_rx==UINT64_C(42949672960));
 assert(a.points[0].rx[0]==300&&a.points[0].tx[0]==600);
 assert(a.d.days[0].key==20261104); /* local midnight UTC+5, not UTC Nov3 */
 assert(!model_due(&a,59));assert(!model_due(&a,299));assert(model_due(&a,301));
 assert(!model_save(&a,argv[1]));
 struct model b;model_init(&b);assert(model_load(&b,argv[1]));assert(b.d.rx==1300&&b.wifi_rx==0);
 uint64_t archive=b.d.days[0].rx;model_reset(&b,1793746810);assert(!model_save(&b,argv[1]));
 assert(b.d.rx-b.d.base_rx==0&&b.d.days[0].rx==archive);
 c[0]+=200;c[1]+=300;model_sample(&b,c,"boot1","table1","",7,1793746806,1);
 assert(b.d.rx-b.d.base_rx==200&&b.d.tx-b.d.base_tx==300);
 assert(b.wifi_rx==0&&b.d.days[0].rx==archive+200);
 assert(!model_save(&b,argv[1]));
 /* Torn latest snapshot: previous complete generation remains readable. */
 char p[512];snprintf(p,sizeof(p),"%s/lte-%u.bin",argv[1],(unsigned)(b.d.generation%2));int fd=open(p,O_WRONLY|O_TRUNC);assert(fd>=0);assert(write(fd,"partial",7)==7);close(fd);
 struct model recovered;model_init(&recovered);assert(model_load(&recovered,argv[1]));assert(recovered.d.generation==b.d.generation-1);
 /* New kernel table starts from zero, never unsigned subtraction from old cursor. */
 uint64_t next[4]={12,34,0,0};uint64_t before=recovered.d.rx;model_sample(&recovered,next,"boot2","table2","",10,0,0);assert(recovered.d.rx==before+12&&recovered.d.undated_rx==12);
 next[0]+=UINT64_C(67108864);model_sample(&recovered,next,"boot2","table2","",13,0,0);recovered.last_save=10;assert(!model_due(&recovered,69));assert(model_due(&recovered,70));
 /* Wall time jumps do not change interval lengths; finite rings stay bounded. */
 for(int i=0;i<400;i++){next[0]+=30;model_sample(&recovered,next,"boot2","table2","",16+i*3,1793746800-i*100,1);}
 assert(recovered.count==POINTS);assert(recovered.points[(recovered.pos+POINTS-1)%POINTS].dt==3);
 /* Wi-Fi history reuses disk v1 in separate files; mobile files stay isolated. */
 struct model wifi;model_init(&wifi);snprintf(wifi.source,sizeof(wifi.source),"wifi");
 uint64_t wc[4]={100,200,0,0};model_sample(&wifi,wc,"boot-w","table-w","",1,1793746800,1);
 assert(wifi.d.rx==100);assert(!model_save(&wifi,argv[1]));
 struct model wifi_loaded;model_init(&wifi_loaded);strcpy(wifi_loaded.source,"wifi");assert(model_load(&wifi_loaded,argv[1])==1);assert(wifi_loaded.d.rx==100);
 struct model mobile_loaded;model_init(&mobile_loaded);assert(model_load(&mobile_loaded,argv[1])==1);assert(mobile_loaded.d.rx!=100);
 uint64_t archive_wifi=wifi.d.days[0].rx;model_reset(&wifi,1793746810);assert(wifi.d.days[0].rx==archive_wifi);
 wc[0]=150;model_sample(&wifi,wc,"boot-w","table-w","",4,1793746803,1);assert(wifi.d.rx-wifi.d.base_rx==50);
 /* Session change resets session volume, never the cumulative counter cursor. */
 struct model sessions;model_init(&sessions);uint64_t sc[4]={0,0,100,0};
 model_sample(&sessions,sc,"boot-s","table-s","ssid-a",1,0,0);assert(sessions.wifi_rx==100);
 sc[2]=150;model_sample(&sessions,sc,"boot-s","table-s","ssid-b",4,0,0);assert(sessions.wifi_rx==50);
 sc[2]=170;model_sample(&sessions,sc,"boot-s","table-s","",7,0,0);assert(sessions.wifi_rx==0);
 sc[2]=200;model_sample(&sessions,sc,"boot-s","table-s","ssid-b",10,0,0);assert(sessions.wifi_rx==30);
 puts("PASS: LTE/Wi-Fi isolation, local calendar, periods/archive, batching, corruption recovery, counter epochs, bounded monotonic series");return 0;
}
