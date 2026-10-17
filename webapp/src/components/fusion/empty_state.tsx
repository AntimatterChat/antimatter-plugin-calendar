// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';

import {isFusion} from './fusion_ui';

import './fusion.css';

type Action = {
    label: string;
    onClick: () => void;
    icon?: React.ReactNode;
};

type Props = {

    // A small line icon, drawn in the app's colour (--am-cc) under Fusion: the app's own icon component.
    icon?: React.ReactNode;
    title?: React.ReactNode;
    text?: React.ReactNode;

    // The one thing to do from here (create...), as Fusion's primary button.
    action?: Action;

    // Still loading: a quiet line, announced to screen readers, instead of the icon and title.
    busy?: boolean;

    // 'danger' for an error.
    tone?: 'app' | 'danger';

    // The classes under the classic UI (the app's own empty state), which keeps its look there.
    classicClassName?: string;
    'data-testid'?: string;
};

/**
 * EmptyState is what an app shows where there's nothing (yet), as Fusion draws its empty states: centred, a small
 * line icon in the app's colour, a short title, a sentence and at most one primary action; or, while loading, one
 * quiet centred line. Under the classic UI it keeps the app's own classes.
 */
export default function EmptyState({icon, title, text, action, busy = false, tone = 'app', classicClassName, 'data-testid': testId}: Props) {
    const fusion = isFusion();
    const className = fusion ? ['am-empty', 'amfx-empty', busy && 'amfx-busy', tone === 'danger' && 'amfx-danger'].filter(Boolean).join(' ') : classicClassName;

    // Fusion's classes for a part, none under the classic UI.
    const part = (name: string) => (fusion ? {className: name} : {});
    return (
        <div
            className={className}
            data-empty-state={busy ? 'busy' : 'true'}
            data-testid={testId}
            {...(busy ? {role: 'status', 'aria-busy': true} : {})}
        >
            {fusion && icon && !busy && <span className='amfx-empty-ic'>{icon}</span>}
            {title && <b {...part('amfx-empty-title')}>{title}</b>}
            {text && <p {...part('amfx-empty-text')}>{text}</p>}
            {action && (
                <button
                    type='button'
                    className={fusion ? 'am-btn am-primary' : 'btn btn-primary'}
                    onClick={action.onClick}
                >
                    {action.icon}
                    {action.label}
                </button>
            )}
        </div>
    );
}
