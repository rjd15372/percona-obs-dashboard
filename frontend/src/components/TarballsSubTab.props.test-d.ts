import TarballsSubTab from './TarballsSubTab.vue'

type TarballsSubTabProps = InstanceType<typeof TarballsSubTab>['$props']

const propsWithLoading: TarballsSubTabProps = {
  tarballs: [],
  loading: true,
}

void propsWithLoading
