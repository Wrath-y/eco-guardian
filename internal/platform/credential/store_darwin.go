//go:build darwin && cgo

package credential

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef eco_string(const char *value) {
	return CFStringCreateWithCString(kCFAllocatorDefault, value, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef eco_query(const char *account) {
	CFStringRef service = eco_string("EcoGuardian");
	CFStringRef accountRef = eco_string(account);
	if (service == NULL || accountRef == NULL) {
		if (service != NULL) CFRelease(service);
		if (accountRef != NULL) CFRelease(accountRef);
		return NULL;
	}
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 4, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (query != NULL) {
		CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
		CFDictionarySetValue(query, kSecAttrService, service);
		CFDictionarySetValue(query, kSecAttrAccount, accountRef);
	}
	CFRelease(service);
	CFRelease(accountRef);
	return query;
}

static int eco_keychain_put(const char *account, const void *bytes, size_t length) {
	CFMutableDictionaryRef query = eco_query(account);
	if (query == NULL) return errSecAllocate;
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, bytes, (CFIndex)length);
	if (data == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecValueData, data);
	OSStatus status = SecItemAdd(query, NULL);
	if (status == errSecDuplicateItem) {
		CFMutableDictionaryRef match = eco_query(account);
		const void *keys[] = { kSecValueData };
		const void *values[] = { data };
		CFDictionaryRef attributes = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		if (match == NULL || attributes == NULL) {
			status = errSecAllocate;
		} else {
			status = SecItemUpdate(match, attributes);
		}
		if (attributes != NULL) CFRelease(attributes);
		if (match != NULL) CFRelease(match);
	}
	CFRelease(data);
	CFRelease(query);
	return status;
}

static int eco_keychain_get(const char *account, void **bytes, size_t *length) {
	*bytes = NULL;
	*length = 0;
	CFMutableDictionaryRef query = eco_query(account);
	if (query == NULL) return errSecAllocate;
	const void *trueValue = kCFBooleanTrue;
	CFDictionarySetValue(query, kSecReturnData, trueValue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef item = NULL;
	OSStatus status = SecItemCopyMatching(query, &item);
	if (status == errSecSuccess) {
		if (item == NULL || CFGetTypeID(item) != CFDataGetTypeID()) {
			status = errSecDecode;
		} else {
			CFIndex size = CFDataGetLength((CFDataRef)item);
			if (size <= 0) {
				status = errSecDecode;
			} else {
				void *copy = malloc((size_t)size);
				if (copy == NULL) {
					status = errSecAllocate;
				} else {
					memcpy(copy, CFDataGetBytePtr((CFDataRef)item), (size_t)size);
					*bytes = copy;
					*length = (size_t)size;
				}
			}
		}
	}
	if (item != NULL) CFRelease(item);
	CFRelease(query);
	return status;
}

static int eco_keychain_delete(const char *account) {
	CFMutableDictionaryRef query = eco_query(account);
	if (query == NULL) return errSecAllocate;
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static int eco_keychain_not_found(void) { return errSecItemNotFound; }
static void eco_keychain_free(void *bytes, size_t length) {
	if (bytes != NULL) {
		memset(bytes, 0, length);
		free(bytes);
	}
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

// DarwinStore stores provider credentials in the user's macOS Keychain.
// Account names are stable Eco Guardian targets and never contain raw
// provider input beyond the validated provider identifier.
type DarwinStore struct{}

func NewDarwinStore() *DarwinStore  { return &DarwinStore{} }
func NewDefaultStore() *DarwinStore { return NewDarwinStore() }

// NewWindowsStore is retained as a source-compatible legacy name. New code
// should use NewDefaultStore so the host credential adapter is selected.
func NewWindowsStore() *DarwinStore { return NewDarwinStore() }

func (store *DarwinStore) Put(ctx context.Context, provider string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil || !validDarwinCredential(value) {
		return aiprovider.ErrCredentialInvalid
	}
	account := C.CString(target)
	defer C.free(unsafe.Pointer(account))
	status := C.eco_keychain_put(account, unsafe.Pointer(&value[0]), C.size_t(len(value)))
	if status != 0 {
		return fmt.Errorf("macOS Keychain write failed (%d)", int(status))
	}
	return nil
}

func (store *DarwinStore) Get(ctx context.Context, provider string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := targetName(provider)
	if err != nil {
		return nil, aiprovider.ErrCredentialInvalid
	}
	account := C.CString(target)
	defer C.free(unsafe.Pointer(account))
	var raw unsafe.Pointer
	var length C.size_t
	status := C.eco_keychain_get(account, &raw, &length)
	if status == C.eco_keychain_not_found() {
		return nil, aiprovider.ErrCredentialNotFound
	}
	if status != 0 || raw == nil || length == 0 || length > C.size_t(aiprovider.MaxCredentialBytes) {
		if raw != nil {
			C.eco_keychain_free(raw, length)
		}
		return nil, fmt.Errorf("macOS Keychain read failed (%d)", int(status))
	}
	value := C.GoBytes(raw, C.int(length))
	C.eco_keychain_free(raw, length)
	if !validDarwinCredential(value) {
		clear(value)
		return nil, aiprovider.ErrCredentialNotFound
	}
	return value, nil
}

func (store *DarwinStore) Delete(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	account := C.CString(target)
	defer C.free(unsafe.Pointer(account))
	status := C.eco_keychain_delete(account)
	if status == C.eco_keychain_not_found() {
		return aiprovider.ErrCredentialNotFound
	}
	if status != 0 {
		return fmt.Errorf("macOS Keychain delete failed (%d)", int(status))
	}
	return nil
}

func validDarwinCredential(value []byte) bool {
	if len(value) == 0 || len(value) > aiprovider.MaxCredentialBytes {
		return false
	}
	for _, character := range value {
		if character == 0 || character == '\r' || character == '\n' {
			return false
		}
	}
	return true
}
