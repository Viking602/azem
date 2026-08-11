//go:build darwin && cgo

package netproxy

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>

static CFDictionaryRef azem_copy_system_proxies(void) {
	return SCDynamicStoreCopyProxies(NULL);
}

static char* azem_copy_proxy_string(const char* rawKey) {
	CFDictionaryRef proxies = azem_copy_system_proxies();
	if (proxies == NULL) return NULL;
	CFStringRef key = CFStringCreateWithCString(NULL, rawKey, kCFStringEncodingUTF8);
	CFTypeRef value = key == NULL ? NULL : CFDictionaryGetValue(proxies, key);
	char* result = NULL;
	if (value != NULL && CFGetTypeID(value) == CFStringGetTypeID()) {
		CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength((CFStringRef)value), kCFStringEncodingUTF8) + 1;
		result = (char*)calloc((size_t)size, 1);
		if (result != NULL && !CFStringGetCString((CFStringRef)value, result, size, kCFStringEncodingUTF8)) {
			free(result);
			result = NULL;
		}
	}
	if (key != NULL) CFRelease(key);
	CFRelease(proxies);
	return result;
}

static int azem_copy_proxy_int(const char* rawKey) {
	CFDictionaryRef proxies = azem_copy_system_proxies();
	if (proxies == NULL) return 0;
	CFStringRef key = CFStringCreateWithCString(NULL, rawKey, kCFStringEncodingUTF8);
	CFTypeRef value = key == NULL ? NULL : CFDictionaryGetValue(proxies, key);
	int result = 0;
	if (value != NULL && CFGetTypeID(value) == CFNumberGetTypeID()) {
		CFNumberGetValue((CFNumberRef)value, kCFNumberIntType, &result);
	} else if (value != NULL && CFGetTypeID(value) == CFBooleanGetTypeID()) {
		result = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
	}
	if (key != NULL) CFRelease(key);
	CFRelease(proxies);
	return result;
}

static int azem_proxy_exception_count(void) {
	CFDictionaryRef proxies = azem_copy_system_proxies();
	if (proxies == NULL) return 0;
	CFTypeRef value = CFDictionaryGetValue(proxies, kSCPropNetProxiesExceptionsList);
	int count = value != NULL && CFGetTypeID(value) == CFArrayGetTypeID() ? (int)CFArrayGetCount((CFArrayRef)value) : 0;
	CFRelease(proxies);
	return count;
}

static char* azem_copy_proxy_exception(int index) {
	CFDictionaryRef proxies = azem_copy_system_proxies();
	if (proxies == NULL) return NULL;
	CFTypeRef value = CFDictionaryGetValue(proxies, kSCPropNetProxiesExceptionsList);
	char* result = NULL;
	if (value != NULL && CFGetTypeID(value) == CFArrayGetTypeID() && index >= 0 && index < CFArrayGetCount((CFArrayRef)value)) {
		CFTypeRef item = CFArrayGetValueAtIndex((CFArrayRef)value, index);
		if (item != NULL && CFGetTypeID(item) == CFStringGetTypeID()) {
			CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength((CFStringRef)item), kCFStringEncodingUTF8) + 1;
			result = (char*)calloc((size_t)size, 1);
			if (result != NULL && !CFStringGetCString((CFStringRef)item, result, size, kCFStringEncodingUTF8)) {
				free(result);
				result = NULL;
			}
		}
	}
	CFRelease(proxies);
	return result;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

func loadSystemProxy() (Settings, error) {
	settings := Settings{
		HTTP: Endpoint{
			Enabled: systemInt("HTTPEnable") != 0,
			Host:    systemString("HTTPProxy"), Port: systemInt("HTTPPort"),
		},
		HTTPS: Endpoint{
			Enabled: systemInt("HTTPSEnable") != 0,
			Host:    systemString("HTTPSProxy"), Port: systemInt("HTTPSPort"),
		},
		SOCKS: Endpoint{
			Enabled: systemInt("SOCKSEnable") != 0,
			Host:    systemString("SOCKSProxy"), Port: systemInt("SOCKSPort"),
		},
		ExcludeSimpleHostnames: systemInt("ExcludeSimpleHostnames") != 0,
	}
	count := int(C.azem_proxy_exception_count())
	if count < 0 || count > 4096 {
		return Settings{}, fmt.Errorf("system proxy exception count is invalid: %d", count)
	}
	settings.Exceptions = make([]string, 0, count)
	for index := 0; index < count; index++ {
		value := C.azem_copy_proxy_exception(C.int(index))
		if value == nil {
			continue
		}
		item := strings.TrimSpace(C.GoString(value))
		C.free(unsafe.Pointer(value))
		if item != "" {
			settings.Exceptions = append(settings.Exceptions, item)
		}
	}
	return settings, nil
}

func systemString(key string) string {
	rawKey := C.CString(key)
	defer C.free(unsafe.Pointer(rawKey))
	value := C.azem_copy_proxy_string(rawKey)
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return strings.TrimSpace(C.GoString(value))
}

func systemInt(key string) int {
	rawKey := C.CString(key)
	defer C.free(unsafe.Pointer(rawKey))
	return int(C.azem_copy_proxy_int(rawKey))
}
