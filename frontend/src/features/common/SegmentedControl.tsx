import React, { useId, useLayoutEffect, useRef, useState } from 'react';
import './segmented-control.css';

interface SegmentOption<T> {
  value: T;
  label: React.ReactNode;
  icon?: React.ReactNode;
  disabled?: boolean;
}

interface SegmentedControlProps<T extends string | number> extends Omit<
  React.HTMLAttributes<HTMLDivElement>,
  'onChange' | 'defaultValue'
> {
  value: T;
  options: readonly SegmentOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  'aria-label': string;
}

export function SegmentedControl<T extends string | number>({
  value,
  options,
  onChange,
  disabled = false,
  className,
  ...props
}: SegmentedControlProps<T>) {
  const name = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef(new Map<T, HTMLLabelElement>());
  const [thumb, setThumb] = useState<{ left: number; width: number } | null>(null);

  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const measure = () => {
      const selected = itemRefs.current.get(value);
      if (!selected) {
        setThumb(null);
        return;
      }
      const bounds = selected.getBoundingClientRect();
      if (!bounds.width) return;
      const left = bounds.left - root.getBoundingClientRect().left + root.scrollLeft;
      const width = bounds.width;
      setThumb(previous =>
        previous?.left === left && previous.width === width ? previous : { left, width },
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(root);
    itemRefs.current.forEach(item => observer.observe(item));
    return () => observer.disconnect();
  }, [value, options]);

  return (
    <div
      {...props}
      ref={rootRef}
      role="radiogroup"
      aria-disabled={disabled || undefined}
      className={['segmented-control', className].filter(Boolean).join(' ')}
    >
      {thumb && (
        <span
          className="segmented-control-thumb"
          aria-hidden="true"
          style={{ width: thumb.width, transform: `translate3d(${thumb.left}px, 0, 0)` }}
        />
      )}
      {options.map(option => {
        const unavailable = disabled || option.disabled;
        return (
          <label
            key={`${typeof option.value}:${option.value}`}
            ref={element => {
              if (element) itemRefs.current.set(option.value, element);
              else itemRefs.current.delete(option.value);
            }}
            className="segmented-control-option"
            data-selected={option.value === value}
            data-disabled={unavailable || undefined}
          >
            <input
              className="segmented-control-input"
              type="radio"
              name={name}
              value={option.value}
              checked={option.value === value}
              disabled={unavailable}
              onChange={event => {
                if (event.target.checked && option.value !== value) onChange(option.value);
              }}
            />
            <span className="segmented-control-label">
              {option.icon && (
                <span className="segmented-control-icon" aria-hidden="true">
                  {option.icon}
                </span>
              )}
              <span className="segmented-control-text">{option.label}</span>
            </span>
          </label>
        );
      })}
    </div>
  );
}
