// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';

import {iconTip} from './fusion/tip';
import Icon from './icon';
import {messages} from './messages';

// CommunityLayerMenu is the Community calls layer's own menu, beside its chip: Subscribe… (the
// layer's private calendar link), its one entry.
export default function CommunityLayerMenu({onSubscribe}: {onSubscribe: () => void}) {
    const {formatMessage} = useIntl();
    const [open, setOpen] = useState(false);
    const ref = useRef<HTMLSpanElement>(null);

    useEffect(() => {
        const onMouseDown = (e: MouseEvent) => {
            if (ref.current && !ref.current.contains(e.target as Node)) {
                setOpen(false);
            }
        };
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.stopPropagation();
                setOpen(false);
            }
        };
        if (open) {
            document.addEventListener('mousedown', onMouseDown);
            document.addEventListener('keydown', onKeyDown, true);
        }
        return () => {
            document.removeEventListener('mousedown', onMouseDown);
            document.removeEventListener('keydown', onKeyDown, true);
        };
    }, [open]);

    return (
        <span ref={ref}>
            <button
                className={am('icon-btn') + ' ' + own('layer-menu-btn')}
                {...iconTip(formatMessage(messages.communityCallsMenu))}
                aria-haspopup='menu'
                aria-expanded={open}
                onClick={() => setOpen((o) => !o)}
            >
                <Icon
                    name='dots'
                    size='sm'
                />
            </button>
            {open && (
                <div
                    className={own('layer-menu')}
                    role='menu'
                    aria-label={formatMessage(messages.communityCallsMenu)}
                >
                    <button
                        role='menuitem'
                        autoFocus={true}
                        onClick={() => {
                            setOpen(false);
                            onSubscribe();
                        }}
                    >
                        {formatMessage(messages.subscribe)}
                    </button>
                </div>
            )}
        </span>
    );
}
