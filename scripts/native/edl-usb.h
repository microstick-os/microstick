#ifndef MICROSTICK_EDL_USB_H
#define MICROSTICK_EDL_USB_H
/* USB topology selects a connection; it is never called chip identity. */
static int microstick_usb_path(libusb_device *dev,char *out,size_t capacity) {
    uint8_t ports[8];int n=libusb_get_port_numbers(dev,ports,sizeof(ports));if(n<=0)return -1;
    int written=snprintf(out,capacity,"@%u-",libusb_get_bus_number(dev));
    if(written<0||(size_t)written>=capacity)return -1;size_t used=(size_t)written;
    for(int i=0;i<n;i++){written=snprintf(out+used,capacity-used,"%s%u",i?".":"",ports[i]);if(written<0||(size_t)written>=capacity-used)return -1;used+=(size_t)written;}
    return 0;
}
#endif
