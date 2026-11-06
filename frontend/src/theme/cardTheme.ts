import type { ThemeConfig } from 'antd';
import type { ConsolePalette } from './palette';

export function createCardTheme(p: ConsolePalette): NonNullable<ThemeConfig['components']>['Card'] {
  return {
    borderRadiusLG: 12,
    colorBgContainer: p.paper,
    colorBorderSecondary: p.panelEdge,
    colorFillAlter: p.paper,
    boxShadowTertiary: 'none',
    headerFontSize: 14,
    headerFontSizeSM: 14,
    headerPadding: 20,
    headerPaddingSM: 16,
    bodyPadding: 20,
    bodyPaddingSM: 16,
  };
}
