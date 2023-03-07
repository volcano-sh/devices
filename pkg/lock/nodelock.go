package lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const MaxLockRetry = 5

var kubeClient kubernetes.Interface

func GetClient() kubernetes.Interface {
	return kubeClient
}

// NewClient connects to an API server
func NewClient() (kubernetes.Interface, error) {
	kubeConfig := os.Getenv("KUBECONFIG")
	if kubeConfig == "" {
		kubeConfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return nil, err
		}
	}
	client, err := kubernetes.NewForConfig(config)
	kubeClient = client
	return client, err
}

// UseClient uses existing client
func UseClient(client kubernetes.Interface) error {
	kubeClient = client
	return nil
}

func setNodeLock(nodeName string, lockName string) error {
	ctx := context.Background()
	node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorln("get node failed", err.Error())
		return err
	}
	if _, ok := node.ObjectMeta.Annotations[lockName]; ok {
		return fmt.Errorf("node %s is locked", nodeName)
	}
	newNode := node.DeepCopy()
	newNode.ObjectMeta.Annotations[lockName] = time.Now().Format(time.RFC3339)
	_, err = kubeClient.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	for i := 0; i < MaxLockRetry && err != nil; i++ {
		klog.ErrorS(err, "Failed to update node", "node", nodeName, "retry", i)
		time.Sleep(100 * time.Millisecond)
		node, err = kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to get node when retry to update", "node", nodeName)
			continue
		}
		newNode := node.DeepCopy()
		newNode.ObjectMeta.Annotations[lockName] = time.Now().Format(time.RFC3339)
		_, err = kubeClient.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("setNodeLock exceeds retry count %d", MaxLockRetry)
	}
	klog.V(3).InfoS("Node lock set", "node", nodeName)
	return nil
}

// ReleaseNodeLock releases a certain lock on a certain device
func ReleaseNodeLock(nodeName string, lockName string) error {
	ctx := context.Background()
	node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, ok := node.ObjectMeta.Annotations[lockName]; !ok {
		klog.V(3).InfoS("Node lock not set", "node", nodeName)
		return nil
	}
	newNode := node.DeepCopy()
	delete(newNode.ObjectMeta.Annotations, lockName)
	_, err = kubeClient.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	for i := 0; i < MaxLockRetry && err != nil; i++ {
		klog.ErrorS(err, "Failed to update node", "node", nodeName, "retry", i)
		time.Sleep(100 * time.Millisecond)
		node, err = kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to get node when retry to update", "node", nodeName)
			continue
		}
		newNode := node.DeepCopy()
		delete(newNode.ObjectMeta.Annotations, lockName)
		_, err = kubeClient.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("releaseNodeLock exceeds retry count %d", MaxLockRetry)
	}
	klog.V(3).InfoS("Node lock released", "node", nodeName)
	return nil
}

// LockNode locks a device on a certain node
func LockNode(nodeName string, lockName string) error {
	ctx := context.Background()
	node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, ok := node.ObjectMeta.Annotations[lockName]; !ok {
		return setNodeLock(nodeName, lockName)
	}
	lockTime, err := time.Parse(time.RFC3339, node.ObjectMeta.Annotations[lockName])
	if err != nil {
		return err
	}
	if time.Since(lockTime) > time.Minute*5 {
		klog.InfoS("Node lock expired", "node", nodeName, "lockTime", lockTime)
		err = ReleaseNodeLock(nodeName, lockName)
		if err != nil {
			klog.ErrorS(err, "Failed to release node lock", "node", nodeName)
			return err
		}
		return setNodeLock(nodeName, lockName)
	}
	return fmt.Errorf("node %s has been locked within 5 minutes", nodeName)
}
