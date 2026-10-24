// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {createIntl} from 'react-intl';

import {
    MAX_COUNT,
    buildRRule,
    defaultRepeat,
    describeRRule,
    parseRRule,
    reminderLabel,
    weekdayIndex,
    type RepeatForm,
} from './recurrence';

const intl = createIntl({locale: 'en', messages: {}});

// Monday 26 October 2026, 14:00 where the test runs.
const start = new Date(2026, 9, 26, 14, 0);

function form(overrides: Partial<RepeatForm>): RepeatForm {
    return {...defaultRepeat(start), ...overrides};
}

describe('buildRRule', () => {
    test('a call that doesn\'t repeat has no rule', () => {
        expect(buildRRule(defaultRepeat(start))).toBe('');
    });

    test('daily, weekly on chosen days and monthly, every N', () => {
        expect(buildRRule(form({freq: 'daily'}))).toBe('FREQ=DAILY');
        expect(buildRRule(form({freq: 'daily', interval: 3}))).toBe('FREQ=DAILY;INTERVAL=3');
        expect(buildRRule(form({freq: 'weekly', days: [3, 0, 3]}))).toBe('FREQ=WEEKLY;BYDAY=MO,TH');
        expect(buildRRule(form({freq: 'weekly', interval: 2, days: [6]}))).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=SU');
        expect(buildRRule(form({freq: 'monthly'}))).toBe('FREQ=MONTHLY');
    });

    test('ending never, on a date (the end of that local day, in UTC) or after N calls', () => {
        expect(buildRRule(form({freq: 'daily', ends: 'never'}))).toBe('FREQ=DAILY');
        expect(buildRRule(form({freq: 'daily', ends: 'after', count: 12}))).toBe('FREQ=DAILY;COUNT=12');

        const until = new Date(2026, 10, 30, 23, 59, 59).toISOString().replace(/\.\d{3}/, '').replace(/[-:]/g, '');
        expect(buildRRule(form({freq: 'weekly', days: [0], ends: 'on', until: '2026-11-30'}))).toBe(`FREQ=WEEKLY;BYDAY=MO;UNTIL=${until}`);
    });

    test('stays within the server\'s bounds', () => {
        expect(buildRRule(form({freq: 'daily', ends: 'after', count: 10000}))).toBe(`FREQ=DAILY;COUNT=${MAX_COUNT}`);
        expect(buildRRule(form({freq: 'daily', ends: 'after', count: 0}))).toBe('FREQ=DAILY;COUNT=1');
        expect(buildRRule(form({freq: 'daily', interval: 0}))).toBe('FREQ=DAILY');
        expect(buildRRule(form({freq: 'daily', interval: Number.NaN}))).toBe('FREQ=DAILY');
    });
});

describe('parseRRule', () => {
    test('reads back what buildRRule writes', () => {
        const cases: RepeatForm[] = [
            form({freq: 'daily', interval: 2}),
            form({freq: 'weekly', days: [0, 3]}),
            form({freq: 'monthly', ends: 'after', count: 6}),
            form({freq: 'weekly', days: [4], ends: 'on', until: '2026-12-31'}),
        ];
        for (const c of cases) {
            expect(parseRRule(buildRRule(c), start)).toEqual(c);
        }
    });

    test('an empty rule is a call that doesn\'t repeat, on the start\'s day', () => {
        expect(parseRRule('', start)).toEqual(defaultRepeat(start));
        expect(defaultRepeat(start).days).toEqual([weekdayIndex(start)]);
        expect(weekdayIndex(start)).toBe(0);
    });

    test('a rule the picker can\'t show is null, to be kept as it is', () => {
        expect(parseRRule('FREQ=YEARLY', start)).toBeNull();
        expect(parseRRule('FREQ=HOURLY', start)).toBeNull();
        expect(parseRRule('FREQ=MONTHLY;BYDAY=1MO', start)).toBeNull();
        expect(parseRRule('FREQ=MONTHLY;BYMONTHDAY=15', start)).toBeNull();
        expect(parseRRule('FREQ=DAILY;BYDAY=MO', start)).toBeNull();
        expect(parseRRule('INTERVAL=2', start)).toBeNull();
    });

    test('an UNTIL that\'s a plain date', () => {
        expect(parseRRule('FREQ=DAILY;UNTIL=20261130', start)).toEqual(form({freq: 'daily', ends: 'on', until: '2026-11-30'}));
    });
});

describe('describeRRule', () => {
    test('says the rule in words', () => {
        expect(describeRRule(intl, 'FREQ=DAILY', start)).toBe('Every day');
        expect(describeRRule(intl, 'FREQ=DAILY;INTERVAL=3', start)).toBe('Every 3 days');
        expect(describeRRule(intl, 'FREQ=WEEKLY;BYDAY=MO,WE', start)).toBe('Every week on Monday and Wednesday');
        expect(describeRRule(intl, 'FREQ=WEEKLY;INTERVAL=2;BYDAY=FR', start)).toBe('Every 2 weeks on Friday');
        expect(describeRRule(intl, 'FREQ=WEEKLY', start)).toBe('Every week on Monday');
        expect(describeRRule(intl, 'FREQ=MONTHLY', start)).toBe('Every month');
        expect(describeRRule(intl, 'FREQ=YEARLY', start)).toBe('Every year');
    });

    test('with how it ends', () => {
        expect(describeRRule(intl, 'FREQ=DAILY;COUNT=10', start)).toBe('Every day, 10 times');
        expect(describeRRule(intl, 'FREQ=DAILY;UNTIL=20261130', start)).toMatch(/^Every day, until .*2026$/);
    });

    test('nothing for a rule it can\'t say', () => {
        expect(describeRRule(intl, 'FREQ=MONTHLY;BYDAY=1MO', start)).toBe('');
    });
});

describe('reminderLabel', () => {
    test('names every choice', () => {
        expect([-1, 0, 5, 10, 15, 30, 60, 1440].map((m) => reminderLabel(intl, m))).toEqual([
            'None', 'When it starts', '5 minutes before', '10 minutes before', '15 minutes before', '30 minutes before', '1 hour before', 'A day before',
        ]);
    });
});
