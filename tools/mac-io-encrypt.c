/* Windows ABI bridge for OpenAbsinthe's extracted XNU property routine.
 * The upstream assembly is fetched separately at a pinned revision; it is not
 * redistributed in this project. No account or network operations occur here.
 */
#include <stdint.h>

extern void __attribute__((sysv_abi)) sub_ffffff8000ec7320(
    const unsigned char *input, uint64_t length, unsigned char *output);

__declspec(dllexport) void mac_io_encrypt(
    const unsigned char *input, uint64_t length, unsigned char *output) {
    sub_ffffff8000ec7320(input, length, output);
}
