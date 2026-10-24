// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {meetingChanged, onMeetingChanged} from './meeting_events';

describe('meeting_events', () => {
    test('tells every listener which meeting changed, and stops once unsubscribed', () => {
        const a = jest.fn();
        const b = jest.fn();
        const unsubscribeA = onMeetingChanged(a);
        onMeetingChanged(b);

        meetingChanged('meeting1', 'channel1');
        expect(a).toHaveBeenCalledWith('meeting1', 'channel1');
        expect(b).toHaveBeenCalledWith('meeting1', 'channel1');

        unsubscribeA();
        meetingChanged('meeting2');
        expect(a).toHaveBeenCalledTimes(1);
        expect(b).toHaveBeenCalledTimes(2);
        expect(b).toHaveBeenLastCalledWith('meeting2', '');
    });
});
