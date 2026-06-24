import ContainersSubTab from './ContainersSubTab.vue'

type ContainersSubTabProps = InstanceType<typeof ContainersSubTab>['$props']

const propsWithLoading: ContainersSubTabProps = {
  containerImages: [],
  copiedKey: null,
  loading: true,
}

void propsWithLoading
