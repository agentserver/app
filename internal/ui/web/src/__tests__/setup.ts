import ElementPlus from 'element-plus';
import { config } from '@vue/test-utils';
config.global.plugins = [ElementPlus];
config.global.stubs = { Loading: true };
