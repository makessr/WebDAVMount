import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {dependencyInventory} from './build/dependency-inventory.mjs'
export default defineConfig({plugins:[vue(), dependencyInventory()]})
