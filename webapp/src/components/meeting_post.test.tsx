// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import type {Post} from '@mattermost/types/posts';

import * as client from '../client';
import type {Meeting} from '../client';
import {meetingChanged} from '../meeting_events';

import MeetingPost from './meeting_post';

jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getMeeting: jest.fn(),
    setRSVP: jest.fn(),
    setReminderOverride: jest.fn(),
    cancelMeeting: jest.fn(),
}));

const mockConfirm = jest.fn();
jest.mock('./fusion/confirm_dialog', () => ({confirmDialog: (...args: unknown[]) => mockConfirm(...args)}));

const mockOpenDialog = jest.fn();
jest.mock('../schedule_call', () => ({openScheduleCallDialog: (...args: unknown[]) => mockOpenDialog(...args)}));

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => null}));
jest.mock('mattermost-redux/selectors/entities/channels', () => ({
    getChannel: () => ({id: 'channel1', name: 'standup', display_name: 'Standup', type: 'O'}),
}));
jest.mock('mattermost-redux/selectors/entities/users', () => ({getCurrentUserId: () => 'organizer1'}));

const mocked = client as jest.Mocked<typeof client>;

function testMeeting(overrides: Partial<Meeting> = {}): Meeting {
    return {
        id: 'meeting1',
        channel_id: 'channel1',
        post_id: 'post1',
        organizer_id: 'organizer1',
        summary: 'Team sync',
        description: '',
        start: Date.parse('2026-10-27T14:00:00Z'),
        end: Date.parse('2026-10-27T14:30:00Z'),
        sequence: 0,
        cancelled: false,
        created_at: Date.parse('2026-10-20T00:00:00Z'),
        updated_at: Date.parse('2026-10-20T00:00:00Z'),
        can_cancel: true,
        ...overrides,
    };
}

function testPost(): Post {
    return {
        id: 'post1',
        channel_id: 'channel1',
        edit_at: 0,
        props: {meeting_id: 'meeting1'},
    } as unknown as Post;
}

function renderPost(post: Post = testPost()) {
    return render(
        <IntlProvider locale='en'>
            <MeetingPost post={post}/>
        </IntlProvider>,
    );
}

afterEach(() => jest.clearAllMocks());

describe('MeetingPost', () => {
    test('shows the title, the time and (as the organiser) Edit and Cancel', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        renderPost();

        expect(await screen.findByText('Team sync')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Cancel the call'})).toBeInTheDocument();
    });

    test('someone who isn\'t the organiser sees RSVP, but not Edit or Cancel', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({organizer_id: 'someone-else', can_cancel: false}));
        renderPost();

        expect(await screen.findByText('Team sync')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Going'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Cancel the call'})).not.toBeInTheDocument();
    });

    test('an admin the server lets cancel (can_cancel) sees Cancel, but never Edit', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({organizer_id: 'someone-else', can_cancel: true}));
        mocked.cancelMeeting.mockResolvedValue(testMeeting({organizer_id: 'someone-else', cancelled: true}));
        mockConfirm.mockResolvedValue(true);
        renderPost();
        await screen.findByText('Team sync');

        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
        act(() => {
            screen.getByRole('button', {name: 'Cancel the call'}).click();
        });

        // A one-off call is cancelled at once (after asking), with no "This call / The whole series" choice.
        await waitFor(() => expect(mocked.cancelMeeting).toHaveBeenCalledWith('meeting1', 'series'));
        expect(mockConfirm).toHaveBeenCalledWith(expect.objectContaining({title: 'Cancel this call?', danger: true}));
        expect(await screen.findByText('Cancelled')).toBeInTheDocument();
    });

    test('Edit opens the dialog with the meeting, for its organiser', async () => {
        const meeting = testMeeting();
        mocked.getMeeting.mockResolvedValue(meeting);
        renderPost();
        await screen.findByText('Team sync');

        act(() => {
            screen.getByRole('button', {name: 'Edit'}).click();
        });
        expect(mockOpenDialog).toHaveBeenCalledWith({channelId: 'channel1', meeting});
    });

    test('RSVP sends the choice and shows it pressed', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        mocked.setRSVP.mockResolvedValue(testMeeting({rsvps: {organizer1: {status: 'maybe', updated_at: 1}}}));
        renderPost();
        await screen.findByText('Team sync');

        const maybe = screen.getByRole('button', {name: 'Maybe'});
        expect(maybe).toHaveAttribute('aria-pressed', 'false');
        act(() => {
            maybe.click();
        });

        await waitFor(() => expect(maybe).toHaveAttribute('aria-pressed', 'true'));
        expect(mocked.setRSVP).toHaveBeenCalledWith('meeting1', 'maybe', false);

        // A one-off call has no "Only the next call".
        expect(screen.queryByLabelText('Only the next call')).not.toBeInTheDocument();
    });

    test('a cancelled meeting shows the badge and hides Join, RSVP and add to calendar', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({cancelled: true}));
        renderPost();

        expect(await screen.findByText('Cancelled')).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Going'})).not.toBeInTheDocument();
        expect(screen.queryByRole('link', {name: 'Add to calendar'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Cancel the call'})).not.toBeInTheDocument();
    });

    test('a series shows its repeat rule in words and its next call', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({
            recurrence_rule: 'FREQ=WEEKLY;BYDAY=TU,TH;COUNT=8',
            next_start: Date.parse('2026-10-29T14:00:00Z'),
            next_end: Date.parse('2026-10-29T14:30:00Z'),
        }));
        renderPost();

        expect(await screen.findByText('Every week on Tuesday and Thursday, 8 times')).toBeInTheDocument();
        expect(screen.getByText(/^Next: /)).toBeInTheDocument();
    });

    test('a series\' RSVP can be for the next call only', async () => {
        const series = testMeeting({recurrence_rule: 'FREQ=DAILY', next_start: Date.parse('2026-10-28T14:00:00Z'), next_end: Date.parse('2026-10-28T14:30:00Z')});
        mocked.getMeeting.mockResolvedValue(series);
        mocked.setRSVP.mockResolvedValue({...series, my_rsvp: 'no', my_rsvp_next_only: true});
        renderPost();
        await screen.findByText('Team sync');

        const nextOnly = screen.getByLabelText('Only the next call');
        expect(nextOnly).not.toBeChecked();
        fireEvent.click(nextOnly);
        act(() => {
            screen.getByRole('button', {name: 'No'}).click();
        });

        await waitFor(() => expect(mocked.setRSVP).toHaveBeenCalledWith('meeting1', 'no', true));
        await waitFor(() => expect(screen.getByRole('button', {name: 'No'})).toHaveAttribute('aria-pressed', 'true'));
        expect(screen.getByLabelText('Only the next call')).toBeChecked();
    });

    test('a series\' Cancel offers "This call" and "The whole series"', async () => {
        const series = testMeeting({recurrence_rule: 'FREQ=WEEKLY', next_start: Date.parse('2026-11-03T14:00:00Z'), next_end: Date.parse('2026-11-03T14:30:00Z')});
        mocked.getMeeting.mockResolvedValue(series);
        mocked.cancelMeeting.mockResolvedValue(series);
        mockConfirm.mockResolvedValue(true);
        renderPost();
        await screen.findByText('Team sync');

        expect(screen.queryByRole('group', {name: 'What to cancel'})).not.toBeInTheDocument();
        act(() => {
            screen.getByRole('button', {name: 'Cancel the call'}).click();
        });
        const choice = screen.getByRole('group', {name: 'What to cancel'});
        expect(choice).toBeInTheDocument();

        act(() => {
            screen.getByRole('button', {name: 'This call'}).click();
        });
        await waitFor(() => expect(mocked.cancelMeeting).toHaveBeenCalledWith('meeting1', 'occurrence'));
        expect(mockConfirm).toHaveBeenCalledWith(expect.objectContaining({title: 'Cancel this call only?'}));

        // Let the cancellation's answer land before asking for the next one.
        await act(async () => {
            await Promise.resolve();
        });

        act(() => {
            screen.getByRole('button', {name: 'Cancel the call'}).click();
        });
        act(() => {
            screen.getByRole('button', {name: 'The whole series'}).click();
        });
        await waitFor(() => expect(mocked.cancelMeeting).toHaveBeenCalledWith('meeting1', 'series'));
        expect(mockConfirm).toHaveBeenLastCalledWith(expect.objectContaining({title: 'Cancel the whole series?'}));
    });

    test('saying no to the confirmation cancels nothing', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        mockConfirm.mockResolvedValue(false);
        renderPost();
        await screen.findByText('Team sync');

        act(() => {
            screen.getByRole('button', {name: 'Cancel the call'}).click();
        });
        await waitFor(() => expect(mockConfirm).toHaveBeenCalled());
        expect(mocked.cancelMeeting).not.toHaveBeenCalled();
    });

    test('"Remind me" shows the call\'s own reminder as Default, and sets or clears the member\'s own', async () => {
        const meeting = testMeeting({reminder_minutes: 15});
        mocked.getMeeting.mockResolvedValue(meeting);
        mocked.setReminderOverride.mockResolvedValue({...meeting, my_reminder_override: 60});
        renderPost();
        await screen.findByText('Team sync');

        const remind = screen.getByLabelText('Remind me') as HTMLSelectElement;
        expect(remind.value).toBe('default');
        expect(screen.getByRole('option', {name: 'Default (15 minutes before)'})).toBeInTheDocument();

        fireEvent.change(remind, {target: {value: '60'}});
        await waitFor(() => expect(mocked.setReminderOverride).toHaveBeenCalledWith('meeting1', 60));
        await waitFor(() => expect((screen.getByLabelText('Remind me') as HTMLSelectElement).value).toBe('60'));

        mocked.setReminderOverride.mockResolvedValue(meeting);
        fireEvent.change(screen.getByLabelText('Remind me'), {target: {value: 'default'}});
        await waitFor(() => expect(mocked.setReminderOverride).toHaveBeenLastCalledWith('meeting1', null));
    });

    test('"Remind me" is hidden from a member who said no', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({my_rsvp: 'no'}));
        renderPost();
        await screen.findByText('Team sync');

        expect(screen.queryByLabelText('Remind me')).not.toBeInTheDocument();
    });

    test('reads the meeting again when the server says it changed', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        renderPost();
        await screen.findByText('Team sync');

        mocked.getMeeting.mockResolvedValue(testMeeting({summary: 'Team sync (moved)'}));
        act(() => meetingChanged('meeting1'));
        expect(await screen.findByText('Team sync (moved)')).toBeInTheDocument();

        // A different meeting changing doesn't trigger a reload of this one.
        mocked.getMeeting.mockClear();
        act(() => meetingChanged('some-other-meeting'));
        expect(mocked.getMeeting).not.toHaveBeenCalled();
    });
});
