#include <assert.h>
#include <stdint.h>
#include <string.h>
#include "meowpad_volume.h"

int main(void) {
    meowpad_volume_state_t state = {0};
    uint8_t packet[32] = {0xAB, 'M', 'P', 1, 42, 0};

    assert(!meowpad_volume_visible(&state, 1));
    assert(!meowpad_volume_receive(&state, packet, 5, 1));
    packet[4] = 101;
    assert(!meowpad_volume_receive(&state, packet, sizeof(packet), 1));
    packet[4] = 42;
    packet[5] = 2;
    assert(!meowpad_volume_receive(&state, packet, sizeof(packet), 1));
    packet[5] = 0;
    assert(meowpad_volume_receive(&state, packet, sizeof(packet), 100));
    assert(state.percent == 42 && !state.muted);
    assert(meowpad_volume_visible(&state, 2099));
    assert(!meowpad_volume_visible(&state, 2100));
    assert(meowpad_volume_receive(&state, packet, sizeof(packet), 2200));
    assert(!meowpad_volume_visible(&state, 2200));
    packet[5] = 1;
    assert(meowpad_volume_receive(&state, packet, sizeof(packet), UINT32_MAX - 50));
    assert(state.muted && meowpad_volume_visible(&state, 25));
    assert(!meowpad_volume_visible(&state, 2000));
    return 0;
}
