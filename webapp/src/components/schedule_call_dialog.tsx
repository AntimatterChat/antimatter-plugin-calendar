// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import ReactDOM from 'react-dom';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import * as client from '../client';
import {
    DEFAULT_REMINDER,
    MAX_COUNT,
    MAX_INTERVAL,
    MAX_REPEAT_DAYS,
    REMINDER_CHOICES,
    buildRRule,
    defaultRepeat,
    parseRRule,
    reminderLabel,
    weekdayIndex,
    weekdayName,
    type RepeatEnds,
    type RepeatForm,
    type RepeatFreq,
} from '../recurrence';
import {onScheduleCallDialog, type ScheduleCallTarget} from '../schedule_call';
import {addDays, dateInput, parseLocal, timeInput} from '../utils';
import {isFusionUI} from '../web_ui';

import {fusionLayer} from './fusion/fusion_ui';
import {messages} from './messages';

const DEFAULT_LEAD_MINUTES = 15;
const DEFAULT_DURATION_MINUTES = 30;

// roundToQuarterHour returns the next quarter hour at or after a date.
function roundToQuarterHour(date: Date): Date {
    const d = new Date(date);
    d.setSeconds(0, 0);
    const remainder = d.getMinutes() % 15;
    if (remainder > 0) {
        d.setMinutes(d.getMinutes() + (15 - remainder));
    }
    return d;
}

// ScheduleCallDialogRoot is the host component of the "Schedule a call" dialog: registered once
// (registerRootComponent), it renders nothing until the channel menu, or a meeting post's own
// Edit button, opens it.
export default function ScheduleCallDialogRoot() {
    const [target, setTarget] = useState<ScheduleCallTarget | null>(null);
    useEffect(() => onScheduleCallDialog(setTarget), []);

    if (!target) {
        return null;
    }
    const layer = (isFusionUI() && fusionLayer()) || document.body;
    return ReactDOM.createPortal(
        <div className={own('root', {fusion: isFusionUI(), classic: !isFusionUI()})}>
            <ScheduleCallForm
                key={target.channelId + (target.meeting?.id || '')}
                target={target}
                onDone={() => setTarget(null)}
            />
        </div>,
        layer,
    );
}

type FormProps = {
    target: ScheduleCallTarget;
    onDone: () => void;
};

// The repeat picker's choices; 'custom' keeps a rule the picker can't show (from another app) as
// it is.
type RepeatChoice = RepeatFreq | 'custom';

function ScheduleCallForm({target, onDone}: FormProps) {
    const intl = useIntl();
    const {formatMessage, locale} = intl;
    const {meeting} = target;

    const initialStart = meeting ? new Date(meeting.start) : roundToQuarterHour(new Date(Date.now() + DEFAULT_LEAD_MINUTES * 60000));
    const initialEnd = meeting ? new Date(meeting.end) : new Date(initialStart.getTime() + DEFAULT_DURATION_MINUTES * 60000);

    const [summary, setSummary] = useState(meeting?.summary || '');
    const [description, setDescription] = useState(meeting?.description || '');
    const [startDate, setStartDate] = useState(dateInput(initialStart));
    const [startTime, setStartTime] = useState(timeInput(initialStart));
    const [endDate, setEndDate] = useState(dateInput(initialEnd));
    const [endTime, setEndTime] = useState(timeInput(initialEnd));
    const parsedRepeat = meeting?.recurrence_rule ? parseRRule(meeting.recurrence_rule, initialStart) : defaultRepeat(initialStart);
    const [repeat, setRepeat] = useState<RepeatForm>(parsedRepeat || defaultRepeat(initialStart));
    const [repeatChoice, setRepeatChoice] = useState<RepeatChoice>(parsedRepeat ? parsedRepeat.freq : 'custom');
    const [reminder, setReminder] = useState(meeting?.reminder_minutes ?? DEFAULT_REMINDER);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');

    const setRepeatField = <K extends keyof RepeatForm>(key: K, value: RepeatForm[K]) => setRepeat((r) => ({...r, [key]: value}));
    const toggleDay = (day: number) => setRepeat((r) => ({...r, days: r.days.includes(day) ? r.days.filter((d) => d !== day) : [...r.days, day]}));
    const unit = {
        none: messages.repeatUnitDays,
        daily: messages.repeatUnitDays,
        weekly: messages.repeatUnitWeeks,
        monthly: messages.repeatUnitMonths,
    }[repeat.freq];

    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.stopPropagation();
                onDone();
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => document.removeEventListener('keydown', onKeyDown, true);
    }, [onDone]);

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        const start = parseLocal(startDate, startTime);
        const end = parseLocal(endDate, endTime);
        if (!(end.getTime() > start.getTime())) {
            setError(formatMessage(messages.scheduleCallEndAfterStart));
            return;
        }

        let rule = '';
        if (repeatChoice === 'custom') {
            rule = meeting?.recurrence_rule || '';
        } else if (repeatChoice !== 'none') {
            if (repeat.freq === 'weekly' && repeat.days.length === 0) {
                setError(formatMessage(messages.repeatNoDays));
                return;
            }
            if (repeat.ends === 'on' && parseLocal(repeat.until).getTime() < parseLocal(startDate).getTime()) {
                setError(formatMessage(messages.repeatUntilBeforeStart));
                return;
            }
            rule = buildRRule(repeat);
        }

        setSaving(true);
        setError('');
        const input: client.MeetingInput = {
            summary: summary.trim(),
            description: description.trim(),
            start: start.getTime(),
            end: end.getTime(),
            time_zone: client.timeZone(),
            recurrence_rule: rule,
            reminder_minutes: reminder,
        };
        try {
            if (meeting) {
                await client.updateMeeting(meeting.id, input);
            } else {
                await client.scheduleCall(target.channelId, input);
            }
            onDone();
        } catch (err) {
            setError((err as Error).message);
            setSaving(false);
        }
    };

    return (
        <div
            className={am('scrim')}
            onMouseDown={(e) => {
                if (e.target === e.currentTarget) {
                    onDone();
                }
            }}
        >
            <form
                className={am('modal', 'small')}
                role='dialog'
                aria-modal='true'
                aria-label={formatMessage(meeting ? messages.editCallTitle : messages.scheduleCallTitle)}
                onSubmit={submit}
            >
                <div className={am('pane')}>
                    <h2>{formatMessage(meeting ? messages.editCallTitle : messages.scheduleCallTitle)}</h2>
                    <p className={am('lead')}>{formatMessage(messages.scheduleCallLead)}</p>
                    <label
                        className={am('field')}
                        htmlFor='amc-call-summary'
                    >
                        <span>{formatMessage(messages.summary)}</span>
                        <input
                            id='amc-call-summary'
                            type='text'
                            required={true}
                            autoFocus={true}
                            maxLength={1000}
                            placeholder={formatMessage(messages.scheduleCallPlaceholder)}
                            value={summary}
                            onChange={(e) => setSummary(e.target.value)}
                        />
                    </label>
                    <div className={own('field-row')}>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-start-date'
                        >
                            <span>{formatMessage(messages.starts)}</span>
                            <input
                                id='amc-call-start-date'
                                type='date'
                                className={own('input')}
                                required={true}
                                value={startDate}
                                onChange={(e) => {
                                    const shift = parseLocal(e.target.value).getTime() - parseLocal(startDate).getTime();
                                    setEndDate(dateInput(new Date(parseLocal(endDate).getTime() + shift)));

                                    // A weekly call on the start's own day follows the start to its new day.
                                    const oldDay = weekdayIndex(parseLocal(startDate));
                                    const newDay = weekdayIndex(parseLocal(e.target.value));
                                    setRepeat((r) => (r.days.length === 1 && r.days[0] === oldDay ? {...r, days: [newDay]} : r));
                                    setStartDate(e.target.value);
                                }}
                            />
                        </label>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-start-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-call-start-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={startTime}
                                onChange={(e) => setStartTime(e.target.value)}
                            />
                        </label>
                    </div>
                    <div className={own('field-row')}>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-end-date'
                        >
                            <span>{formatMessage(messages.ends)}</span>
                            <input
                                id='amc-call-end-date'
                                type='date'
                                className={own('input')}
                                required={true}
                                min={startDate}
                                value={endDate}
                                onChange={(e) => setEndDate(e.target.value)}
                            />
                        </label>
                        <label
                            className={am('field')}
                            htmlFor='amc-call-end-time'
                        >
                            <span>{' '}</span>
                            <input
                                id='amc-call-end-time'
                                type='time'
                                className={own('input')}
                                required={true}
                                value={endTime}
                                onChange={(e) => setEndTime(e.target.value)}
                            />
                        </label>
                    </div>
                    <label
                        className={am('field')}
                        htmlFor='amc-call-repeat'
                    >
                        <span>{formatMessage(messages.repeat)}</span>
                        <select
                            id='amc-call-repeat'
                            className={own('input')}
                            value={repeatChoice}
                            onChange={(e) => {
                                const choice = e.target.value as RepeatChoice;
                                setRepeatChoice(choice);
                                if (choice !== 'custom') {
                                    setRepeatField('freq', choice);
                                }
                            }}
                        >
                            <option value='none'>{formatMessage(messages.repeatNone)}</option>
                            <option value='daily'>{formatMessage(messages.repeatDaily)}</option>
                            <option value='weekly'>{formatMessage(messages.repeatWeekly)}</option>
                            <option value='monthly'>{formatMessage(messages.repeatMonthly)}</option>
                            {repeatChoice === 'custom' && <option value='custom'>{formatMessage(messages.repeatCustom)}</option>}
                        </select>
                    </label>
                    {repeatChoice !== 'none' && repeatChoice !== 'custom' && (
                        <div className={own('repeat-options')}>
                            <div className={own('field-row')}>
                                <div className={own('inline')}>
                                    <label
                                        className={am('field')}
                                        htmlFor='amc-call-interval'
                                    >
                                        <span>{formatMessage(messages.repeatEvery)}</span>
                                        <input
                                            id='amc-call-interval'
                                            type='number'
                                            className={own('input', 'number')}
                                            min={1}
                                            max={MAX_INTERVAL}
                                            required={true}
                                            value={repeat.interval}
                                            onChange={(e) => setRepeatField('interval', Number(e.target.value))}
                                        />
                                    </label>
                                    <span className={own('unit')}>{formatMessage(unit, {count: repeat.interval})}</span>
                                </div>
                                <label
                                    className={am('field')}
                                    htmlFor='amc-call-repeat-ends'
                                >
                                    <span>{formatMessage(messages.repeatEnds)}</span>
                                    <select
                                        id='amc-call-repeat-ends'
                                        className={own('input')}
                                        value={repeat.ends}
                                        onChange={(e) => setRepeatField('ends', e.target.value as RepeatEnds)}
                                    >
                                        <option value='never'>{formatMessage(messages.repeatEndsNever)}</option>
                                        <option value='on'>{formatMessage(messages.repeatEndsOn)}</option>
                                        <option value='after'>{formatMessage(messages.repeatEndsAfter)}</option>
                                    </select>
                                </label>
                            </div>
                            {repeat.freq === 'weekly' && (
                                <div
                                    className={own('weekdays')}
                                    role='group'
                                    aria-label={formatMessage(messages.repeatOn)}
                                >
                                    {[0, 1, 2, 3, 4, 5, 6].map((day) => (
                                        <button
                                            key={day}
                                            type='button'
                                            className={am('chip-btn', {on: repeat.days.includes(day)})}
                                            aria-pressed={repeat.days.includes(day)}
                                            aria-label={weekdayName(day, locale)}
                                            onClick={() => toggleDay(day)}
                                        >
                                            {weekdayName(day, locale, 'short')}
                                        </button>
                                    ))}
                                </div>
                            )}
                            {repeat.ends === 'on' && (
                                <label
                                    className={am('field')}
                                    htmlFor='amc-call-repeat-until'
                                >
                                    <span>{formatMessage(messages.repeatUntil)}</span>
                                    <input
                                        id='amc-call-repeat-until'
                                        type='date'
                                        className={own('input')}
                                        required={true}
                                        min={startDate}
                                        max={dateInput(addDays(parseLocal(startDate), MAX_REPEAT_DAYS))}
                                        value={repeat.until}
                                        onChange={(e) => setRepeatField('until', e.target.value)}
                                    />
                                </label>
                            )}
                            {repeat.ends === 'after' && (
                                <label
                                    className={am('field')}
                                    htmlFor='amc-call-repeat-count'
                                >
                                    <span>{formatMessage(messages.repeatCount)}</span>
                                    <input
                                        id='amc-call-repeat-count'
                                        type='number'
                                        className={own('input', 'number')}
                                        min={1}
                                        max={MAX_COUNT}
                                        required={true}
                                        value={repeat.count}
                                        onChange={(e) => setRepeatField('count', Number(e.target.value))}
                                    />
                                </label>
                            )}
                        </div>
                    )}
                    <label
                        className={am('field')}
                        htmlFor='amc-call-reminder'
                    >
                        <span>{formatMessage(messages.alarm)}</span>
                        <select
                            id='amc-call-reminder'
                            className={own('input')}
                            value={String(reminder)}
                            onChange={(e) => setReminder(Number(e.target.value))}
                        >
                            {REMINDER_CHOICES.map((minutes) => (
                                <option
                                    key={minutes}
                                    value={String(minutes)}
                                >
                                    {reminderLabel(intl, minutes)}
                                </option>
                            ))}
                        </select>
                    </label>
                    <label
                        className={am('field')}
                        htmlFor='amc-call-description'
                    >
                        <span>{formatMessage(messages.description)}</span>
                        <textarea
                            id='amc-call-description'
                            maxLength={4000}
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                        />
                    </label>
                    {error && <div className={own('error')}>{error}</div>}
                    <div className={am('modal-actions')}>
                        <button
                            type='button'
                            className={am('btn')}
                            onClick={onDone}
                        >
                            {formatMessage(messages.cancel)}
                        </button>
                        <button
                            type='submit'
                            className={am('btn', 'primary')}
                            disabled={saving}
                        >
                            {formatMessage(saving ? messages.saving : (meeting ? messages.save : messages.scheduleCallSubmit))}
                        </button>
                    </div>
                </div>
            </form>
        </div>
    );
}
