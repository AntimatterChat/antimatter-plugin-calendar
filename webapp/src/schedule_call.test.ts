// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {onScheduleCallDialog, openScheduleCallDialog} from './schedule_call';

describe('schedule_call', () => {
    test('tells every listener what to open, and stops once unsubscribed', () => {
        const a = jest.fn();
        const b = jest.fn();
        const unsubscribeA = onScheduleCallDialog(a);
        onScheduleCallDialog(b);

        openScheduleCallDialog({channelId: 'channel1'});
        expect(a).toHaveBeenCalledWith({channelId: 'channel1'});
        expect(b).toHaveBeenCalledWith({channelId: 'channel1'});

        unsubscribeA();
        openScheduleCallDialog({channelId: 'channel2', meeting: {id: 'm1'} as never});
        expect(a).toHaveBeenCalledTimes(1);
        expect(b).toHaveBeenCalledTimes(2);
        expect(b).toHaveBeenLastCalledWith({channelId: 'channel2', meeting: {id: 'm1'}});
    });
});
