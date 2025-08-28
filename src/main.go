package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	jsonpatch "gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
)

// PhaseTiming 定义阶段时间数据结构
type PhaseTiming struct {
	Phase string `json:"phase"`
	Time  string `json:"time"`
}

// PVCTiming 定义 PVC timing 数据结构
type PVCTiming struct {
	CurrentPhase string        `json:"currentPhase"`
	Timings      []PhaseTiming `json:"timings"`
}

// PVCController 定义PVC控制器结构体
type PVCController struct {
	clientset   *kubernetes.Clientset
	indexer     cache.Indexer
	informer    cache.Controller
	stopCh      chan struct{}
	workqueue   workqueue.RateLimitingInterface
	pvcHandling sync.Map // 用于跟踪正在处理的PVC
}

// NewPVCController 创建新的PVC控制器
func NewPVCController(clientset *kubernetes.Clientset) *PVCController {
	return &PVCController{
		clientset: clientset,
		stopCh:    make(chan struct{}),
		workqueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.DefaultControllerRateLimiter(),
			"pvcs",
		),
	}
}

// Start 启动PVC控制器
func (c *PVCController) Start() error {
	// 创建PVC的ListWatch
	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return c.clientset.CoreV1().PersistentVolumeClaims("").List(context.Background(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return c.clientset.CoreV1().PersistentVolumeClaims("").Watch(context.Background(), options)
		},
	}

	// 创建informer
	c.indexer, c.informer = cache.NewIndexerInformer(listWatch, &corev1.PersistentVolumeClaim{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onPVCAdd,
		UpdateFunc: c.onPVCUpdate,
		DeleteFunc: c.onPVCDelete,
	}, cache.Indexers{})

	// 启动informer
	go c.informer.Run(c.stopCh)

	// 等待缓存同步
	if !cache.WaitForCacheSync(c.stopCh, c.informer.HasSynced) {
		return fmt.Errorf("failed to sync PVC cache")
	}

	// 启动工作线程
	for i := 0; i < 3; i++ { // 启动3个worker处理队列
		go wait.Until(c.runWorker, time.Second, c.stopCh)
	}

	log.Println("PVC controller started successfully")
	return nil
}

// Stop 停止PVC控制器
func (c *PVCController) Stop() {
	c.workqueue.ShutDown()
	close(c.stopCh)
}

// onPVCAdd 处理PVC添加事件
func (c *PVCController) onPVCAdd(obj interface{}) {
	pvc := obj.(*corev1.PersistentVolumeClaim)
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("Error getting key for PVC: %v", err)
		return
	}
	c.workqueue.Add(key)
	log.Printf("PVC added to queue: %s, Phase: %s", key, pvc.Status.Phase)
}

// onPVCUpdate 处理PVC更新事件
func (c *PVCController) onPVCUpdate(oldObj, newObj interface{}) {
	oldPVC := oldObj.(*corev1.PersistentVolumeClaim)
	newPVC := newObj.(*corev1.PersistentVolumeClaim)

	// 检查状态是否发生变化
	if oldPVC.Status.Phase != newPVC.Status.Phase {
		key, err := cache.MetaNamespaceKeyFunc(newObj)
		if err != nil {
			log.Printf("Error getting key for PVC: %v", err)
			return
		}
		c.workqueue.Add(key)
		log.Printf("PVC status changed added to queue: %s, Phase: %s -> %s",
			key, oldPVC.Status.Phase, newPVC.Status.Phase)
	}
}

// onPVCDelete 处理PVC删除事件
func (c *PVCController) onPVCDelete(obj interface{}) {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			log.Printf("Error decoding object, invalid type")
			return
		}
		pvc, ok = tombstone.Obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			log.Printf("Error decoding tombstone object, invalid type")
			return
		}
	}
	log.Printf("PVC deleted: %s/%s", pvc.Namespace, pvc.Name)
}

// runWorker 处理工作队列中的项目
func (c *PVCController) runWorker() {
	for c.processNextItem() {
	}
}

// processNextItem 处理下一个队列项目
func (c *PVCController) processNextItem() bool {
	key, quit := c.workqueue.Get()
	if quit {
		return false
	}
	defer c.workqueue.Done(key)

	// 检查是否正在处理此PVC
	if _, loaded := c.pvcHandling.LoadOrStore(key, true); loaded {
		// 如果已经在处理，重新入队列稍后处理
		c.workqueue.AddRateLimited(key)
		log.Printf("PVC %s is already being processed, re-queuing", key)
		return true
	}
	defer c.pvcHandling.Delete(key)

	err := c.processPVC(key.(string))
	if err != nil {
		log.Printf("Error processing PVC %s: %v", key, err)
		// 重新入队列重试
		c.workqueue.AddRateLimited(key)
	} else {
		// 处理成功，重置限速器
		c.workqueue.Forget(key)
	}

	return true
}

// processPVC 处理具体的PVC
func (c *PVCController) processPVC(key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return fmt.Errorf("error fetching object with key %s from store: %v", key, err)
	}

	if !exists {
		// PVC不存在，可能是被删除了
		log.Printf("PVC %s does not exist anymore", key)
		return nil
	}

	pvc := obj.(*corev1.PersistentVolumeClaim)
	return c.checkAndAnnotatePVC(pvc)
}

// checkAndAnnotatePVC 检查PVC状态并在完成时添加注解
func (c *PVCController) checkAndAnnotatePVC(pvc *corev1.PersistentVolumeClaim) error {
	// 检查PVC是否处于完成状态
	if pvc.Status.Phase == corev1.ClaimBound {
		// 检查是否已经有webhook-trigger注解
		if _, exists := pvc.Annotations["webhook-trigger"]; !exists {
			// 使用重试机制处理可能的冲突
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				// 获取最新的PVC对象
				latestPVC, err := c.clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(
					context.Background(), pvc.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}

				// 再次检查注解
				if _, exists := latestPVC.Annotations["webhook-trigger"]; exists {
					return nil // 注解已存在，无需处理
				}

				// 添加webhook-trigger注解
				timestamp := fmt.Sprintf("%d", time.Now().Unix())

				// 创建patch来添加注解
				patch := []byte(fmt.Sprintf(
					`[{"op":"add","path":"/metadata/annotations/webhook-trigger","value":"%s"}]`,
					timestamp))

				// 应用patch
				_, err = c.clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Patch(
					context.Background(),
					pvc.Name,
					types.JSONPatchType,
					patch,
					metav1.PatchOptions{},
				)
				return err
			})

			if err != nil {
				return fmt.Errorf("failed to add webhook-trigger annotation to PVC %s/%s: %v",
					pvc.Namespace, pvc.Name, err)
			}

			log.Printf("Successfully added webhook-trigger annotation to PVC %s/%s: %s",
				pvc.Namespace, pvc.Name, time.Now().Format(time.RFC3339))
		}
	}
	return nil
}

// createKubernetesClient 创建Kubernetes客户端
func createKubernetesClient() (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	// 尝试从集群内部配置创建客户端
	config, err = rest.InClusterConfig()
	if err != nil {
		log.Printf("Failed to get in-cluster config: %v", err)

		// 尝试从kubeconfig文件创建客户端
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Kubernetes config: %v", err)
		}
		log.Println("Using kubeconfig file for Kubernetes client")
	} else {
		log.Println("Using in-cluster config for Kubernetes client")
	}

	// 创建clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %v", err)
	}

	return clientset, nil
}

func mutateHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received webhook request: %s %s", r.Method, r.URL.Path)

	var review admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		log.Printf("Failed to decode AdmissionReview: %v", err)
		http.Error(w, "Failed to decode AdmissionReview", http.StatusBadRequest)
		return
	}

	req := review.Request
	resp := &admissionv1.AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	// 设置响应版本信息
	reviewResponse := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: resp,
	}

	// 只处理 PVC
	log.Printf("Processing request: Kind=%s, Operation=%s, Name=%s", req.Kind.Kind, req.Operation, req.Name)

	if req.Kind.Kind != "PersistentVolumeClaim" {
		log.Printf("Skipping non-PVC request: %s", req.Kind.Kind)
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	// 对于 DELETE 操作，直接返回，不需要修改
	if req.Operation == admissionv1.Delete {
		log.Printf("PVC %s: DELETE operation, skipping mutation", req.Name)
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	// 解析原始 PVC 对象
	pvc := &corev1.PersistentVolumeClaim{}
	if err := json.Unmarshal(req.Object.Raw, pvc); err != nil {
		log.Printf("Failed to unmarshal PVC: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	log.Printf("PVC %s/%s: Operation=%s, Current Phase=%s",
		pvc.Namespace, pvc.Name, req.Operation, pvc.Status.Phase)

	// 创建修改后的对象
	patchedPVC := pvc.DeepCopy()
	if patchedPVC.Annotations == nil {
		patchedPVC.Annotations = make(map[string]string)
	}

	// 使用 RFC3339 格式的时间字符串
	now := time.Now().Format(time.RFC3339)
	currentPhase := string(patchedPVC.Status.Phase)

	// 获取之前的 timing 数据
	var previousTiming PVCTiming
	if timingStr, exists := patchedPVC.Annotations["pvc-timing"]; exists {
		if err := json.Unmarshal([]byte(timingStr), &previousTiming); err != nil {
			log.Printf("Failed to parse previous timing: %v", err)
			previousTiming = PVCTiming{
				CurrentPhase: currentPhase,
				Timings:      []PhaseTiming{},
			}
		}
	} else {
		previousTiming = PVCTiming{
			CurrentPhase: currentPhase,
			Timings:      []PhaseTiming{},
		}
	}

	// 检查阶段是否发生变化
	phaseChanged := previousTiming.CurrentPhase != currentPhase

	// 构建新的 timing 数据
	newTiming := PVCTiming{
		CurrentPhase: currentPhase,
		Timings:      previousTiming.Timings,
	}

	// 如果阶段发生变化，添加新的时间记录
	if phaseChanged {
		newTiming.Timings = append(newTiming.Timings, PhaseTiming{
			Phase: currentPhase,
			Time:  now,
		})
		log.Printf("PVC %s/%s: Phase changed from %s to %s, adding timing record",
			pvc.Namespace, pvc.Name, previousTiming.CurrentPhase, currentPhase)
	} else {
		log.Printf("PVC %s/%s: Phase unchanged (%s), updating existing records",
			pvc.Namespace, pvc.Name, currentPhase)
	}

	// 对于 CREATE 操作，确保至少有一个时间记录
	if req.Operation == admissionv1.Create && len(newTiming.Timings) == 0 {
		newTiming.Timings = append(newTiming.Timings, PhaseTiming{
			Phase: currentPhase,
			Time:  now,
		})
		log.Printf("PVC %s/%s: CREATE operation, adding initial timing record",
			pvc.Namespace, pvc.Name)
	}

	// 对于 UPDATE 操作，如果当前阶段不在已有的记录中，也添加记录
	if req.Operation == admissionv1.Update {
		phaseExists := false
		for _, timing := range newTiming.Timings {
			if timing.Phase == currentPhase {
				phaseExists = true
				break
			}
		}

		if !phaseExists {
			newTiming.Timings = append(newTiming.Timings, PhaseTiming{
				Phase: currentPhase,
				Time:  now,
			})
			log.Printf("PVC %s/%s: UPDATE operation, adding new phase timing record for %s",
				pvc.Namespace, pvc.Name, currentPhase)
		}
	}

	// 序列化 timing 数据
	timingJSON, err := json.Marshal(newTiming)
	if err != nil {
		log.Printf("Failed to marshal timing data: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	// 设置 annotation
	patchedPVC.Annotations["pvc-timing"] = string(timingJSON)
	log.Printf("PVC %s/%s: Setting pvc-timing annotation to: %s",
		pvc.Namespace, pvc.Name, string(timingJSON))

	// 创建 JSON patch
	originalJSON, err := json.Marshal(pvc)
	if err != nil {
		log.Printf("Failed to marshal original PVC: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	modifiedJSON, err := json.Marshal(patchedPVC)
	if err != nil {
		log.Printf("Failed to marshal modified PVC: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	patch, err := jsonpatch.CreatePatch(originalJSON, modifiedJSON)
	if err != nil {
		log.Printf("Failed to create patch: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		log.Printf("Failed to marshal patch: %v", err)
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Message: err.Error(),
		}
		json.NewEncoder(w).Encode(reviewResponse)
		return
	}

	log.Printf("PVC %s/%s applying patches: %s", pvc.Namespace, pvc.Name, string(patchJSON))

	// 设置响应
	resp.Patch = patchJSON
	patchType := admissionv1.PatchTypeJSONPatch
	resp.PatchType = &patchType

	json.NewEncoder(w).Encode(reviewResponse)
}

func main() {
	TLS_CERT_FILE := os.Getenv("TLS_CERT_FILE")
	TLS_KEY_FILE := os.Getenv("TLS_KEY_FILE")

	if TLS_CERT_FILE == "" || TLS_KEY_FILE == "" {
		log.Fatal("TLS_CERT_FILE and TLS_KEY_FILE must be set")
	}

	// 创建Kubernetes客户端
	clientset, err := createKubernetesClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// 创建并启动PVC控制器
	pvcController := NewPVCController(clientset)
	if err := pvcController.Start(); err != nil {
		log.Fatalf("Failed to start PVC controller: %v", err)
	}
	defer pvcController.Stop()

	http.HandleFunc("/mutate", mutateHandler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	log.Println("Starting PVC Mutating Webhook on :8080")
	log.Fatal(http.ListenAndServeTLS(":8080", TLS_CERT_FILE, TLS_KEY_FILE, nil))
}
