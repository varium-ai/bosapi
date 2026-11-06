import { useLayoutEffect, useRef, useState } from 'react';
import { Tooltip } from 'antd';
import './overflow-text.css';

export function OverflowText({
  children,
  className,
  as: Element = 'span',
}: {
  children: string;
  className?: string;
  as?: 'span' | 'div';
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [overflowing, setOverflowing] = useState(false);
  const [open, setOpen] = useState(false);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    let active = true;
    const measure = () => {
      if (!active) return;
      const clipped = element.scrollWidth > element.clientWidth;
      setOverflowing(clipped);
      if (!clipped) setOpen(false);
    };
    setOpen(false);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    window.addEventListener('resize', measure);
    document.fonts?.addEventListener('loadingdone', measure);
    void document.fonts?.ready.then(measure);
    return () => {
      active = false;
      observer.disconnect();
      window.removeEventListener('resize', measure);
      document.fonts?.removeEventListener('loadingdone', measure);
    };
  }, [children]);

  return (
    <Tooltip
      title={overflowing ? children : undefined}
      trigger={['hover', 'focus']}
      open={overflowing && open}
      onOpenChange={setOpen}
    >
      <Element
        ref={ref}
        className={['console-overflow-text', className].filter(Boolean).join(' ')}
        tabIndex={overflowing ? 0 : undefined}
        onKeyDown={event => {
          if (event.key === 'Escape' && open) {
            setOpen(false);
            event.stopPropagation();
          }
        }}
      >
        {children}
      </Element>
    </Tooltip>
  );
}
