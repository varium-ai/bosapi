import type { ThemeConfig } from 'antd';
import type { ConsolePalette } from './palette';

export function createButtonTheme(
  p: ConsolePalette,
): NonNullable<ThemeConfig['components']>['Button'] {
  return {
    controlHeight: 36,
    controlHeightSM: 30,
    borderRadius: 8,
    borderRadiusSM: 6,
    contentFontSize: 13,
    fontWeight: 500,
    paddingInline: 11,
    iconGap: 6,
    defaultShadow: 'none',
    primaryShadow: 'none',
    dangerShadow: 'none',
    primaryColor: p.paper,
    colorPrimary: p.ink,
    colorPrimaryHover: p.primaryHover,
    colorPrimaryActive: p.primaryHover,
    defaultBorderColor: p.line,
    defaultColor: p.ink,
    defaultBg: p.paper,
    defaultHoverBg: p.fill,
    defaultHoverColor: p.ink,
    defaultHoverBorderColor: p.focus,
    defaultActiveBg: p.fill,
    defaultActiveColor: p.ink,
    defaultActiveBorderColor: p.focus,
    textTextColor: p.gray,
    textTextHoverColor: p.ink,
    textTextActiveColor: p.ink,
    textHoverBg: p.fill,
    colorBgTextActive: p.fill,
  };
}
